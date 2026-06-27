// Package httpapi: 对话用量合计 handler（GET /conversations/:id/usage）。
//
// 与 llm_invocation_handler.go（按 hunter 分组的明细列表）不同，本 handler 给前端会话头部
// 提供"本对话累计 token / 耗时"的权威合计——直接 SUM llm_invocation + tool_invocation，
// 而非前端按 SSE 事件求和（后者漏掉"无文字纯 tool_call"调用，见 reasoning_callback.go）。
package httpapi

import (
	"context"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/V3teran/liusha/internal/llminvocation"
	"github.com/V3teran/liusha/internal/toolinvocation"
)

// UsageOwnerResolver 把对话 id 解析成 owner id + 查运行态 + 墙钟时长（*conversation.Store 满足）。
type UsageOwnerResolver interface {
	ResolveOwnerID(ctx context.Context, convID string) (string, error)
	IsRunActive(ctx context.Context, convID string) (bool, error)
	RunStatus(ctx context.Context, convID string) (string, error)
	WallclockMs(ctx context.Context, convID string) (int64, error)
}

// LLMUsageAggregator 合计某 owner 的 LLM 用量（*llminvocation.Store 满足）。
type LLMUsageAggregator interface {
	Flush(ctx context.Context) error
	AggregateByOwner(ctx context.Context, ownerID string) (llminvocation.Aggregate, error)
}

// ToolUsageAggregator 合计某 owner 的工具用量（*toolinvocation.Store 满足）。
type ToolUsageAggregator interface {
	AggregateByOwner(ctx context.Context, ownerID string) (toolinvocation.Aggregate, error)
}

// conversationUsageHandler 处理 GET /conversations/:id/usage。
//
// 响应（前端会话头部 token/耗时 chip 消费）：
//
//	{
//	  "conversation_id": "...",
//	  "owner_id": "...",                 // 纯聊天对话为空
//	  "tokens": { "in": N, "out": N, "cached": N, "total": N },
//	  "llm_latency_ms": N,               // 所有 LLM 调用耗时合计
//	  "tool_duration_ms": N,             // 所有工具执行耗时合计
//	  "duration_ms": N,                  // = llm_latency_ms + tool_duration_ms（总耗时）
//	  "llm_calls": N, "tool_calls": N,
//	  "running": bool                    // 是否仍有运行中的扫描（权威：owner 终态）
//	}
func conversationUsageHandler(conv UsageOwnerResolver, llm LLMUsageAggregator, tool ToolUsageAggregator) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		if id == "" {
			c.JSON(400, gin.H{"error": "id required"})
			return
		}
		ctx := c.Request.Context()
		ownerID, err := conv.ResolveOwnerID(ctx, id)
		if err != nil {
			if strings.Contains(err.Error(), "no rows in result set") {
				c.JSON(404, gin.H{"error": "conversation not found", "conversation_id": id})
				return
			}
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}

		// 运行态（权威）：active_scan / passive_session 是否仍 active。前端据此显示"工作中"。
		running, err := conv.IsRunActive(ctx, id)
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		// 真实三态（active/completed/aborted）：顶部状态栏据此区分「已完成 vs 已中止」，
		// 不再用二元 running（它把 aborted 错显示成已完成）。
		runStatus, err := conv.RunStatus(ctx, id)
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}

		// 墙钟时长：发起→完成的真实流逝时间（"我等了多久"），跑中用 now-created。
		wallclockMs, err := conv.WallclockMs(ctx, id)
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}

		// 纯聊天（无关联 scan / passive_session）→ 零用量。
		if ownerID == "" {
			c.JSON(200, zeroUsage(id, "", running, runStatus))
			return
		}

		// 先 flush 异步 buffer，保证拿到最新落库的调用（支撑前端实时刷新口径一致）。
		_ = llm.Flush(ctx)
		la, err := llm.AggregateByOwner(ctx, ownerID)
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		ta, err := tool.AggregateByOwner(ctx, ownerID)
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}

		c.JSON(200, gin.H{
			"conversation_id": id,
			"owner_id":        ownerID,
			"tokens": gin.H{
				"in":     la.InTokens,
				"out":    la.OutTokens,
				"cached": la.CachedTokens,
				"total":  la.InTokens + la.OutTokens,
			},
			"llm_latency_ms":   la.LatencyMs,
			"tool_duration_ms": ta.DurationMs,
			// duration_ms = 墙钟（发起→完成真实流逝），前端"耗时"展示用此。
			// work_ms = Σ(LLM latency + 工具 duration)，因子代理并发累加 > 墙钟，仅作明细参考。
			"duration_ms": wallclockMs,
			"work_ms":     la.LatencyMs + ta.DurationMs,
			"llm_calls":   la.Calls,
			"tool_calls":  ta.Calls,
			"running":     running,
			"status":      runStatus, // 真实三态 active/completed/aborted（顶部状态栏三态显示用）
		})
	}
}

// zeroUsage 造零用量响应（纯聊天对话无 owner 时）。
func zeroUsage(convID, ownerID string, running bool, status string) gin.H {
	return gin.H{
		"conversation_id":  convID,
		"owner_id":         ownerID,
		"tokens":           gin.H{"in": 0, "out": 0, "cached": 0, "total": 0},
		"llm_latency_ms":   0,
		"tool_duration_ms": 0,
		"duration_ms":      0,
		"work_ms":          0,
		"llm_calls":        0,
		"tool_calls":       0,
		"running":          running,
		"status":           status,
	}
}
