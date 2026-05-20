// Package httpapi: LLM invocation 审计 handler（按 agent_run_id 分组）。
package httpapi

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/V3teran/liusha/internal/llminvocation"
)

// InvocationsAPI 是 handler 依赖的窄接口；*llminvocation.Store 自动满足。
type InvocationsAPI interface {
	Flush(ctx context.Context) error
	ListByOwnerID(ctx context.Context, ownerID string) ([]llminvocation.Invocation, error)
}

// llmInvocationsHandler 处理 GET /llm/invocations/:owner_id。
//
// 返回该 owner 下所有 llm_invocation 行，**按 agent_run_id 分组**，
// 每组内按 created_at ASC（与 react step 顺序一致）。agent_run_id 为 NULL
// 的归到 "unassigned" 分组。
//
// 响应结构（前端 viewer 消费）：
//
//	{
//	  "owner_id": "...",
//	  "total": 19,
//	  "groups": [
//	    {
//	      "agent_run_id": "65287081-773b-...",
//	      "count": 11,
//	      "invocations": [{...所有字段...}, ...]
//	    }
//	  ]
//	}
//
// 先调 Flush() 等异步 buffer commit，保证拿到完整审计。
func llmInvocationsHandler(api InvocationsAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		eid := c.Param("owner_id")
		if eid == "" {
			c.JSON(400, gin.H{"error": "owner_id required"})
			return
		}
		_ = api.Flush(c.Request.Context())

		invocations, err := api.ListByOwnerID(c.Request.Context(), eid)
		if err != nil {
			if strings.Contains(err.Error(), "no rows in result set") {
				c.JSON(404, gin.H{"error": "owner not found", "owner_id": eid})
				return
			}
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}

		// 按 agent_run_id 分组；保持组首次出现顺序。
		groups := make(map[string][]gin.H)
		order := make([]string, 0)
		for _, v := range invocations {
			key := "unassigned"
			if v.TaskID != nil && *v.TaskID != "" {
				key = *v.TaskID
			}
			if _, exists := groups[key]; !exists {
				order = append(order, key)
			}
			// messages / result 是 []byte 形式的 jsonb——用 json.RawMessage
			// 包装让 gin 直接 inline JSON，而不是 base64 编码成字符串。
			groups[key] = append(groups[key], gin.H{
				"id":           v.ID,
				"agent_run_id": v.TaskID,
				"owner_type":   v.OwnerType,
				"owner_id":     v.OwnerID,
				"provider":     v.Provider,
				"model":         v.Model,
				"in_tokens":     v.InTokens,
				"out_tokens":    v.OutTokens,
				"cached_tokens": v.CachedTokens,
				"cost_usd":      v.CostUSD,
				"latency_ms":    v.LatencyMs,
				"finish_reason": v.FinishReason,
				"error_message": v.Error,
				"call_purpose":  v.CallPurpose,
				"messages":      json.RawMessage(rawOrEmpty(v.Messages, "[]")),
				"result":        json.RawMessage(rawOrEmpty(v.Result, "{}")),
				"created_at":    v.CreatedAt,
			})
		}

		out := make([]gin.H, 0, len(order))
		for _, k := range order {
			out = append(out, gin.H{
				"agent_run_id": k,
				"count":        len(groups[k]),
				"invocations":  groups[k],
			})
		}

		c.JSON(200, gin.H{
			"owner_id": eid,
			"total":         len(invocations),
			"groups":        out,
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
