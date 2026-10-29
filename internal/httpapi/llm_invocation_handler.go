// Package httpapi: LLM invocation 审计 handler（按 agent_id 分组；列表/详情/统计三端点分离）。

package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"github.com/V3teran/liusha/internal/llminvocation"
)

// InvocationsAPI 是 handler 依赖的窄接口；*llminvocation.Store 自动满足。
type InvocationsAPI interface {
	Flush(ctx context.Context) error
	ListAll(ctx context.Context, f llminvocation.ListFilter) ([]llminvocation.Invocation, error)
	CountAll(ctx context.Context, f llminvocation.ListFilter) (int, error)
	AggregateAll(ctx context.Context, f llminvocation.ListFilter) (llminvocation.Aggregate, error)
	GetDetailByID(ctx context.Context, id int64) (llminvocation.Invocation, error)
}

// defaultInvocationPageSize / maxInvocationPageSize：/llm/invocations/:task_id 分页默认值/上限。
// ListByTask 自身也有硬上限（防御纵深），这里的上限对齐它，避免 handler 层看似能开更大窗口。
const (
	defaultInvocationPageSize = 200
	maxInvocationPageSize     = 1000
)

// parseInvocationFilter 从 query 解析筛选条件（列表/统计共用，保证口径一致）。
//
//	q=<search> role=<角色> model=<模型> only_err=1 start=<RFC3339> end=<RFC3339> page=<n> size=<n>
//
// 时间用 RFC3339（前端 Date.toISOString() 直出）；解析失败的时间视为未传，不报错——
// 审计筛选是查询辅助，宁可退化成不筛也不要因为一个坏参数把整页打成 400。
//
// offset 分页（非游标）：对齐流量/漏洞模块的「共 X 条 + N/M 页」体验，配合 Count 算总页数。
func parseInvocationFilter(c *gin.Context, page, size int) llminvocation.ListFilter {
	f := llminvocation.ListFilter{
		Search:  c.Query("q"), // 允许为空：空 = 全量（按时间倒序），全局审计不强制先选对话
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

// toolNamesOrEmpty 把 nil 切片归一成空数组——前端渲染工具徽章时不必判空。
func toolNamesOrEmpty(names []string) []string {
	if names == nil {
		return []string{}
	}
	return names
}

// llmInvocationsSearchHandler 处理 GET /llm/invocations/search?q=<search>&page=&size=&role=&model=&only_err=&start=&end=。
//
// 全局搜索端点：支持按 task_id/conversation_id/assignment_id 搜索 LLM 调用。
// q 为空时返回所有数据（按时间倒序），不需要先选对话，适用于全局审计场景。
//
// 响应结构：
//
//	{
//	  "total": 19, "page": 1, "size": 200,
//	  "items": [{...含 conversation_id/assignment_id}]
//	}
func llmInvocationsSearchHandler(api InvocationsAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		page, size := parsePagination(c.Query("page"), c.Query("size"), defaultInvocationPageSize, maxInvocationPageSize)
		f := parseInvocationFilter(c, page, size)

		if flushErr := api.Flush(c.Request.Context()); flushErr != nil {
			apiLog.Warn().Err(flushErr).Msg("llm 审计补写失败（审计行可能缺失）")
		}

		invocations, err := api.ListAll(c.Request.Context(), f)
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		total, err := api.CountAll(c.Request.Context(), f)
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}

		items := make([]gin.H, 0, len(invocations))
		for _, v := range invocations {
			items = append(items, gin.H{
				"id":              v.ID,
				"request_id":      v.RequestID,
				"agent_run_id":    v.AgentRunID,
				"task_id":         v.TaskID,
				"conversation_id": v.ConversationID,
				"assignment_id":   v.AssignmentID,
				"provider":        v.Provider,
				"model":           v.Model,
				"in_tokens":       v.InTokens,
				"out_tokens":      v.OutTokens,
				"cached_tokens":   v.CachedTokens,
				"latency_ms":      v.LatencyMs,
				"finish_reason":   v.FinishReason,
				"error_message":   v.Error,
				"role":            v.Role,
				"created_at":      v.CreatedAt,
				"tool_names":      toolNamesOrEmpty(v.ToolNames),
				"text_preview":    v.TextPreview,
			})
		}

		c.JSON(http.StatusOK, gin.H{
			"total": total,
			"page":  page,
			"size":  size,
			"items": items,
		})
	}
}

// llmInvocationsSearchStatHandler 处理 GET /llm/invocations/search/stat?q=&role=&model=&only_err=&start=&end=。
//
// 全局搜索口径的用量合计（整个筛选结果集，与翻到第几页无关）——审计页头部统计 chip
// 的数据源。此前前端用当页行自行 reduce 聚合，50 条/页时「调用 50」与分页器
// 「共 197 条」同屏打架；列表与统计吃同一份 parseInvocationFilter，口径永不打架。
func llmInvocationsSearchStatHandler(api InvocationsAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		f := parseInvocationFilter(c, 0, 0) // 统计不吃分页

		if flushErr := api.Flush(c.Request.Context()); flushErr != nil {
			apiLog.Warn().Err(flushErr).Msg("llm 审计补写失败（审计行可能缺失）")
		}

		a, err := api.AggregateAll(c.Request.Context(), f)
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"calls":         a.Calls,
			"in_tokens":     a.InTokens,
			"out_tokens":    a.OutTokens,
			"cached_tokens": a.CachedTokens,
			"latency_ms":    a.LatencyMs,
		})
	}
}

// llmInvocationDetailByIDHandler 处理 GET /llm/invocations/detail/:id。
//
// 通过 invocation ID 直接获取详情（不需要 task_id），用于全局搜索后点击详情的场景。
func llmInvocationDetailByIDHandler(api InvocationsAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		idStr := c.Param("id")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil || id <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
			return
		}
		if flushErr := api.Flush(c.Request.Context()); flushErr != nil {
			apiLog.Warn().Err(flushErr).Msg("llm 审计补写失败（审计行可能缺失）")
		}

		v, err := api.GetDetailByID(c.Request.Context(), id)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				c.JSON(http.StatusNotFound, gin.H{"error": "invocation not found", "id": id})
				return
			}
			respondErr(c, http.StatusInternalServerError, err)
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"id":              v.ID,
			"request_id":      v.RequestID,
			"agent_run_id":    v.AgentRunID,
			"task_id":         v.TaskID,
			"conversation_id": v.ConversationID,
			"assignment_id":   v.AssignmentID,
			"source":          v.Source,
			"provider":        v.Provider,
			"model":           v.Model,
			"in_tokens":       v.InTokens,
			"out_tokens":      v.OutTokens,
			"cached_tokens":   v.CachedTokens,
			"latency_ms":      v.LatencyMs,
			"finish_reason":   v.FinishReason,
			"error_message":   v.Error,
			"role":            v.Role,
			"messages":        json.RawMessage(rawOrEmpty(v.Messages, "[]")),
			"result":          json.RawMessage(rawOrEmpty(v.Result, "{}")),
			"created_at":      v.CreatedAt,
		})
	}
}
