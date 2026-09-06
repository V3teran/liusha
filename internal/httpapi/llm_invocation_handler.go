// Package httpapi: LLM invocation 审计 handler（按 agent_id 分组；列表/详情/统计三端点分离）。
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"github.com/V3teran/liusha/internal/llminvocation"
)

// InvocationsAPI 是 handler 依赖的窄接口；*llminvocation.Store 自动满足。
type InvocationsAPI interface {
	Flush(ctx context.Context) error
	ListByTask(ctx context.Context, taskID string, f llminvocation.ListFilter) ([]llminvocation.Invocation, error)
	CountByTask(ctx context.Context, taskID string, f llminvocation.ListFilter) (int, error)
	GetByID(ctx context.Context, taskID string, id int64) (llminvocation.Invocation, error)
	AggregateByTask(ctx context.Context, taskID string, f llminvocation.ListFilter) (llminvocation.Aggregate, error)
	FacetsByTask(ctx context.Context, taskID string) (llminvocation.Facets, error)
}

// defaultInvocationPageSize / maxInvocationPageSize：/llm/invocations/:task_id 分页默认值/上限。
// ListByTask 自身也有硬上限（防御纵深），这里的上限对齐它，避免 handler 层看似能开更大窗口。
const (
	defaultInvocationPageSize = 200
	maxInvocationPageSize     = 1000
)

// parseInvocationFilter 从 query 解析筛选条件（列表与统计共用，保证两者口径一致）。
//
//	role=<角色> model=<模型> only_err=1 start=<RFC3339> end=<RFC3339> page=<n> size=<n>
//
// 时间用 RFC3339（前端 Date.toISOString() 直出）；解析失败的时间视为未传，不报错——
// 审计筛选是查询辅助，宁可退化成不筛也不要因为一个坏参数把整页打成 400。
//
// offset 分页（非游标）：对齐流量/漏洞模块的「共 X 条 + N/M 页」体验，配合 CountByTask 算总页数。
func parseInvocationFilter(c *gin.Context, page, size int) llminvocation.ListFilter {
	f := llminvocation.ListFilter{
		Role:    c.Query("role"),
		Model:   c.Query("model"),
		OnlyErr: c.Query("only_err") == "1" || c.Query("only_err") == "true",
	}
	if s := c.Query("start"); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			f.Start = &t
		}
	}
	if s := c.Query("end"); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			f.End = &t
		}
	}
	if size > 0 {
		f.Limit = size
		f.Offset = (page - 1) * size
	}
	return f
}

// parseInvocationPaging 解析 page/size query 参数：page 缺省/非法 = 1；size 缺省
// defaultInvocationPageSize，超 maxInvocationPageSize 收敛到上限。
func parseInvocationPaging(c *gin.Context) (page, size int) {
	page, _ = strconv.Atoi(c.Query("page"))
	if page < 1 {
		page = 1
	}
	size, _ = strconv.Atoi(c.Query("size"))
	if size <= 0 || size > maxInvocationPageSize {
		size = defaultInvocationPageSize
	}
	return page, size
}

// llmInvocationsHandler 处理 GET /llm/invocations/:task_id?page=&size=&role=&model=&only_err=&start=&end=。
//
// offset 分页（非游标）：对齐流量/漏洞模块的「共 X 条 + N/M 页」体验，配合 CountByTask 算总页数。
// **不返回 messages/result**（大字段，列表页从不展示，详情走
// GET /llm/invocations/:task_id/invocation/:id 按需拉）。
//
// 返回**扁平 items**，不再按 agent_id 分组：审计表的可读性来自单表密度 + 单元格内 badge
// （对齐业界日志页做法），而非把一张表切成 N 段——分组既压缩不了信息量，又让跨 agent 的
// 时序对比、排序、筛选全部失效。agent_id/role 作为普通列随行返回，需要聚合看时用筛选。
//
// 响应结构（前端消费）：
//
//	{
//	  "task_id": "...", "total": 19, "page": 1, "size": 200,
//	  "items": [{...不含 messages/result}]
//	}
func llmInvocationsHandler(api InvocationsAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		eid := c.Param("task_id")
		if eid == "" {
			c.JSON(400, gin.H{"error": "task_id required"})
			return
		}
		page, size := parseInvocationPaging(c)
		f := parseInvocationFilter(c, page, size)
		_ = api.Flush(c.Request.Context())

		invocations, err := api.ListByTask(c.Request.Context(), eid, f)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) || strings.Contains(err.Error(), "no rows") {
				c.JSON(404, gin.H{"error": "task not found", "task_id": eid})
				return
			}
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		total, err := api.CountByTask(c.Request.Context(), eid, f)
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}

		items := make([]gin.H, 0, len(invocations))
		for _, v := range invocations {
			items = append(items, gin.H{
				"id":            v.ID,
				"request_id":    v.RequestID,
				"agent_id":      v.ExecutorID,
				"task_id":       v.TaskID,
				"provider":      v.Provider,
				"model":         v.Model,
				"in_tokens":     v.InTokens,
				"out_tokens":    v.OutTokens,
				"cached_tokens": v.CachedTokens,
				"latency_ms":    v.LatencyMs,
				"ttft_ms":       v.TTFTMs,
				"is_stream":     v.IsStream,
				"finish_reason": v.FinishReason,
				"error_message": v.Error,
				"role":          v.Role,
				"created_at":    v.CreatedAt,
				// 派生摘要（库内算，不传 result 大字段）：这次调用产出了什么。
				"tool_names":   toolNamesOrEmpty(v.ToolNames),
				"text_preview": v.TextPreview,
			})
		}

		c.JSON(200, gin.H{
			"task_id": eid,
			"total":   total,
			"page":    page,
			"size":    size,
			"items":   items,
		})
	}
}

// llmInvocationDetailHandler 处理 GET /llm/invocations/:task_id/invocation/:id。
//
// 返回单条 invocation 的完整行（含 messages/result 原文），供列表页点击钻取。
// id+task_id 联合定位，防跨 task 猜 id 越权读取审计原文。
func llmInvocationDetailHandler(api InvocationsAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		eid := c.Param("task_id")
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if eid == "" || err != nil {
			c.JSON(400, gin.H{"error": "task_id/id required"})
			return
		}
		v, err := api.GetByID(c.Request.Context(), eid, id)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) || strings.Contains(err.Error(), "no rows") {
				c.JSON(404, gin.H{"error": "invocation not found"})
				return
			}
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{
			"id":            v.ID,
			"request_id":    v.RequestID,
			"agent_id":      v.ExecutorID,
			"task_id":       v.TaskID,
			"provider":      v.Provider,
			"model":         v.Model,
			"in_tokens":     v.InTokens,
			"out_tokens":    v.OutTokens,
			"cached_tokens": v.CachedTokens,
			"latency_ms":    v.LatencyMs,
			"ttft_ms":       v.TTFTMs,
			"is_stream":     v.IsStream,
			"finish_reason": v.FinishReason,
			"error_message": v.Error,
			"role":          v.Role,
			"messages":      json.RawMessage(rawOrEmpty(v.Messages, "[]")),
			"result":        json.RawMessage(rawOrEmpty(v.Result, "{}")),
			"created_at":    v.CreatedAt,
		})
	}
}

// llmInvocationStatHandler 处理 GET /llm/invocations/:task_id/stat?role=&model=&only_err=&start=&end=。
//
// 数据库层 SUM 聚合（复用 AggregateByTask，会话用量端点已验证过的口径），
// 前端汇总卡不再对全量 invocations 数组做客户端 reduce。
// 吃与列表**同一套筛选参数**（不含分页游标）：筛选后统计跟着变，避免「明细 3 条、合计全量」。
//
// 不回传会话相关字段：用量页的职责是「花了多少、快不快、失败没有」，
// 不引导用户去看对话内容（那是会话模块的职责）——业界日志页同样如此。
func llmInvocationStatHandler(api InvocationsAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		eid := c.Param("task_id")
		if eid == "" {
			c.JSON(400, gin.H{"error": "task_id required"})
			return
		}
		_ = api.Flush(c.Request.Context())
		// 统计吃与列表同一套筛选，但不分页（page/size=0 → parseInvocationFilter 不设 Limit/Offset）。
		a, err := api.AggregateByTask(c.Request.Context(), eid, parseInvocationFilter(c, 1, 0))
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{
			"task_id":       eid,
			"calls":         a.Calls,
			"in_tokens":     a.InTokens,
			"out_tokens":    a.OutTokens,
			"cached_tokens": a.CachedTokens,
			"latency_ms":    a.LatencyMs,
		})
	}
}

// llmInvocationFacetsHandler 处理 GET /llm/invocations/:task_id/facets。
//
// 返回该 task 下 role / model 的候选集合，供前端筛选下拉。必须服务端算——分页下前端只见当前页，
// 从已加载行推候选会漏掉后续页的值。不吃筛选参数（候选恒为该 task 全集，否则筛完下拉自锁死）。
func llmInvocationFacetsHandler(api InvocationsAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		eid := c.Param("task_id")
		if eid == "" {
			c.JSON(400, gin.H{"error": "task_id required"})
			return
		}
		_ = api.Flush(c.Request.Context())
		f, err := api.FacetsByTask(c.Request.Context(), eid)
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		// nil slice 会序列化成 null；统一成 []，前端不必判空。
		roles, models := f.Roles, f.Models
		if roles == nil {
			roles = []string{}
		}
		if models == nil {
			models = []string{}
		}
		c.JSON(200, gin.H{"task_id": eid, "roles": roles, "models": models})
	}
}

// toolNamesOrEmpty 把 nil 切片归一成空数组——前端渲染工具徽章时不必判空。
func toolNamesOrEmpty(names []string) []string {
	if names == nil {
		return []string{}
	}
	return names
}
