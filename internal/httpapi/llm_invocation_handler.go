// Package httpapi: LLM invocation 审计 handler（按 hunter_id 分组；列表/详情/统计三端点分离）。
package httpapi

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/V3teran/liusha/internal/llminvocation"
)

// InvocationsAPI 是 handler 依赖的窄接口；*llminvocation.Store 自动满足。
type InvocationsAPI interface {
	Flush(ctx context.Context) error
	ListByTask(ctx context.Context, taskID string, afterID int64, limit int) ([]llminvocation.Invocation, error)
	GetByID(ctx context.Context, taskID string, id int64) (llminvocation.Invocation, error)
	AggregateByTask(ctx context.Context, taskID string) (llminvocation.Aggregate, error)
}

// defaultInvocationPageSize / maxInvocationPageSize：/llm/invocations/:task_id 分页默认值/上限。
// ListByTask 自身也有硬上限（防御纵深），这里的上限对齐它，避免 handler 层看似能开更大窗口。
const (
	defaultInvocationPageSize = 200
	maxInvocationPageSize     = 1000
)

// llmInvocationsHandler 处理 GET /llm/invocations/:task_id?after=<id>&limit=<n>。
//
// 按 id 游标翻页（keyset，非 offset——避免无界查询）；**不返回 messages/result**（大字段，
// 列表页从不展示，详情走 GET /llm/invocations/:task_id/invocation/:id 按需拉）。
// 仍按 hunter_id 分组（一次 hunter 执行批次），组内保留每行 role——同一 hunter 批次可能混多个
// role（如 orchestrator 内联跑 exploitation 不建独立 hunter 行），分组标题不能只标一个 role，
// 前端从组内逐行 role 派生展示。
//
// 响应结构（前端消费）：
//
//	{
//	  "task_id": "...", "total": 19, "next_after": 1093, "has_more": false,
//	  "groups": [
//	    {"hunter_id": "65287081-773b-...", "count": 11, "invocations": [{...不含 messages/result}]}
//	  ]
//	}
func llmInvocationsHandler(api InvocationsAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		eid := c.Param("task_id")
		if eid == "" {
			c.JSON(400, gin.H{"error": "task_id required"})
			return
		}
		afterID, _ := strconv.ParseInt(c.Query("after"), 10, 64)
		limit, _ := strconv.Atoi(c.Query("limit"))
		if limit <= 0 || limit > maxInvocationPageSize {
			limit = defaultInvocationPageSize
		}
		_ = api.Flush(c.Request.Context())

		invocations, err := api.ListByTask(c.Request.Context(), eid, afterID, limit)
		if err != nil {
			if strings.Contains(err.Error(), "no rows in result set") {
				c.JSON(404, gin.H{"error": "task not found", "task_id": eid})
				return
			}
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}

		// 按 hunter_id 分组；保持组首次出现顺序。
		groups := make(map[string][]gin.H)
		order := make([]string, 0)
		for _, v := range invocations {
			key := "unassigned"
			if v.HunterID != nil && *v.HunterID != "" {
				key = *v.HunterID
			}
			if _, exists := groups[key]; !exists {
				order = append(order, key)
			}
			groups[key] = append(groups[key], gin.H{
				"id":            v.ID,
				"request_id":    v.RequestID,
				"hunter_id":     v.HunterID,
				"task_id":       v.TaskID,
				"provider":      v.Provider,
				"model":         v.Model,
				"in_tokens":     v.InTokens,
				"out_tokens":    v.OutTokens,
				"cached_tokens": v.CachedTokens,
				"latency_ms":    v.LatencyMs,
				"finish_reason": v.FinishReason,
				"error_message": v.Error,
				"role":          v.Role,
				"created_at":    v.CreatedAt,
			})
		}

		out := make([]gin.H, 0, len(order))
		for _, k := range order {
			out = append(out, gin.H{
				"hunter_id":   k,
				"count":       len(groups[k]),
				"invocations": groups[k],
			})
		}

		var nextAfter int64
		if len(invocations) > 0 {
			nextAfter = invocations[len(invocations)-1].ID
		}
		c.JSON(200, gin.H{
			"task_id":    eid,
			"total":      len(invocations),
			"next_after": nextAfter,
			"has_more":   len(invocations) == limit, // 拉满一页才可能有下一页；不满页即到底
			"groups":     out,
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
			if strings.Contains(err.Error(), "no rows in result set") {
				c.JSON(404, gin.H{"error": "invocation not found"})
				return
			}
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{
			"id":            v.ID,
			"request_id":    v.RequestID,
			"hunter_id":     v.HunterID,
			"task_id":       v.TaskID,
			"provider":      v.Provider,
			"model":         v.Model,
			"in_tokens":     v.InTokens,
			"out_tokens":    v.OutTokens,
			"cached_tokens": v.CachedTokens,
			"latency_ms":    v.LatencyMs,
			"finish_reason": v.FinishReason,
			"error_message": v.Error,
			"role":          v.Role,
			"messages":      json.RawMessage(rawOrEmpty(v.Messages, "[]")),
			"result":        json.RawMessage(rawOrEmpty(v.Result, "{}")),
			"created_at":    v.CreatedAt,
		})
	}
}

// llmInvocationStatHandler 处理 GET /llm/invocations/:task_id/stat。
//
// 数据库层 SUM 聚合（复用 AggregateByTask，会话用量端点已验证过的口径），
// 前端汇总卡不再对全量 invocations 数组做客户端 reduce。
func llmInvocationStatHandler(api InvocationsAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		eid := c.Param("task_id")
		if eid == "" {
			c.JSON(400, gin.H{"error": "task_id required"})
			return
		}
		_ = api.Flush(c.Request.Context())
		a, err := api.AggregateByTask(c.Request.Context(), eid)
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

// rawOrEmpty 兜底空 []byte——避免前端拿到 null 而是合法 JSON 字面量。
func rawOrEmpty(b []byte, empty string) []byte {
	if len(b) == 0 {
		return []byte(empty)
	}
	return b
}
