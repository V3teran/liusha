// Package httpapi: 模型调用记录（llm_invocation）的只读接口：列表、统计、分面与单条详情。

package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/V3teran/liusha/internal/llminvocation"
)

// InvocationsAPI 是 handler 依赖的窄接口；*llminvocation.Store 自动满足。
type InvocationsAPI interface {
	Flush(ctx context.Context) error
	ListAll(ctx context.Context, f llminvocation.ListFilter) ([]llminvocation.Invocation, error)
	CountAll(ctx context.Context, f llminvocation.ListFilter) (int, error)
	AggregateAll(ctx context.Context, f llminvocation.ListFilter) (llminvocation.Aggregate, error)
	Facets(ctx context.Context, f llminvocation.ListFilter) (llminvocation.Facets, error)
	GetDetailByID(ctx context.Context, id int64) (llminvocation.Invocation, error)
}

// defaultInvocationPageSize / maxInvocationPageSize：列表分页默认值/上限。
// ListByTask 自身也有硬上限（防御纵深），这里的上限对齐它，避免 handler 层看似能开更大窗口。
const (
	defaultInvocationPageSize = 200
	maxInvocationPageSize     = 1000
)

// parseInvocationFilter 从 query 解析筛选条件（列表/统计/分面共用，保证口径一致）。
//
//	search=<关键词> role=<角色> model=<模型> only_err=1 start=<RFC3339> end=<RFC3339> page=<n> size=<n>
//	task=<任务 ID> conversation=<对话 ID>
//
// 时间用 RFC3339（前端 Date.toISOString() 直出）；写错时返回错误（handler 回 400）。
// task / conversation 是范围：从任务或对话跳来时只看它名下的调用（两者语义见 ListFilter）。
//
// offset 分页（非游标）：对齐流量/漏洞模块的「共 X 条 + N/M 页」体验，配合 Count 算总页数。
func parseInvocationFilter(c *gin.Context, page, size int) (llminvocation.ListFilter, error) {
	f := llminvocation.ListFilter{
		Search:  c.Query("search"), // 允许为空：空 = 全量（按 id 倒序）
		Role:    c.Query("role"),
		Model:   c.Query("model"),
		OnlyErr: c.Query("only_err") == "1" || c.Query("only_err") == "true",
	}
	// 范围按 uuid 比较，不是合法的 ID 在这里就报出，不把它带进 SQL
	for param, dst := range map[string]*string{"task": &f.TaskID, "conversation": &f.ConversationID} {
		if v := c.Query(param); v != "" {
			if _, err := uuid.Parse(v); err != nil {
				return f, fmt.Errorf("参数 %s 不是合法的 ID", param)
			}
			*dst = v
		}
	}
	var err error
	if f.Start, err = timeQuery(c, "start"); err != nil {
		return f, err
	}
	if f.End, err = timeQuery(c, "end"); err != nil {
		return f, err
	}
	if size > 0 {
		f.Limit = size
		f.Offset = (page - 1) * size
	}
	return f, nil
}

// listLlmInvocationsHandler 处理 GET /llm/invocations（筛选参数见 parseInvocationFilter）。
//
// search 为空时返回所有数据（按 id 倒序）；非空时按 task_id/conversation_id/assignment_id 搜索。
//
// 响应结构：
//
//	{
//	  "total": 19, "page": 1, "size": 200,
//	  "items": [{...含 conversation_id/assignment_id}]
//	}
func listLlmInvocationsHandler(api InvocationsAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		page, size := parsePagination(c.Query("page"), c.Query("size"), defaultInvocationPageSize, maxInvocationPageSize)
		f, err := parseInvocationFilter(c, page, size)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		if flushErr := api.Flush(c.Request.Context()); flushErr != nil {
			apiLog.Warn().Err(flushErr).Msg("模型日志补写失败（日志行可能缺失）")
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
				"error_kind":      v.ErrorKind,
				"role":            v.Role,
				"created_at":      v.CreatedAt,
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

// llmInvocationsStatHandler 处理 GET /llm/invocations/stat（与列表同一套筛选参数）。
//
// 当前筛选口径的用量合计（整个筛选结果集，与翻到第几页无关）——模型调用页头部统计
// 的数据源。此前前端用当页行自行 reduce 聚合，50 条/页时「调用 50」与分页器
// 「共 197 条」同屏打架；列表与统计吃同一份 parseInvocationFilter，口径永不打架。
func llmInvocationsStatHandler(api InvocationsAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		f, err := parseInvocationFilter(c, 0, 0) // 统计不吃分页
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		if flushErr := api.Flush(c.Request.Context()); flushErr != nil {
			apiLog.Warn().Err(flushErr).Msg("模型日志补写失败（日志行可能缺失）")
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

// llmInvocationsFacetsHandler 处理 GET /llm/invocations/facets（与列表同一套筛选参数）：
// 角色 / 模型筛选的选项，来自整个筛选结果集而非当前页，每个维度不受自身筛选影响。
func llmInvocationsFacetsHandler(api InvocationsAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		f, err := parseInvocationFilter(c, 0, 0)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		facets, err := api.Facets(c.Request.Context(), f)
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"roles": facets.Roles, "models": facets.Models})
	}
}

// getLlmInvocationHandler 处理 GET /llm/invocations/:id：按调用记录的 ID 取单条详情。
//
// id 是自增的 bigint（不是 UUID），格式与取值在这里校验。
func getLlmInvocationHandler(api InvocationsAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		idStr := c.Param("id")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil || id <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
			return
		}
		if flushErr := api.Flush(c.Request.Context()); flushErr != nil {
			apiLog.Warn().Err(flushErr).Msg("模型日志补写失败（日志行可能缺失）")
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
			"error_kind":      v.ErrorKind,
			"role":            v.Role,
			"messages":        json.RawMessage(rawOrEmpty(v.Messages, "[]")),
			"result":          json.RawMessage(rawOrEmpty(v.Result, "{}")),
			"created_at":      v.CreatedAt,
		})
	}
}
