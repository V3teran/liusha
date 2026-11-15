package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/V3teran/liusha/internal/llminvocation"
	"github.com/V3teran/liusha/internal/toolinvocation"
)

// LLMUsageAggregator 合计某任务的 LLM 用量（*llminvocation.Store 满足）。
type LLMUsageAggregator interface {
	// Flush 把异步缓冲的调用记录落库，合计前调用，拿到的是最新口径。
	Flush(ctx context.Context) error
	AggregateByTask(ctx context.Context, taskID string, f llminvocation.ListFilter) (llminvocation.Aggregate, error)
}

// ToolUsageAggregator 合计某任务的工具用量（*toolinvocation.Store 满足）。
type ToolUsageAggregator interface {
	AggregateByTask(ctx context.Context, taskID string) (toolinvocation.Aggregate, error)
}

// taskUsage 是 GET /tasks/:id/usage 的响应：任务的用量合计。
//
// elapsed_ms 是墙钟（发起→结束，扣除续接前的停顿），即「等了多久」；llm_latency_ms 与
// tool_duration_ms 是各调用耗时之和，子代理并发时两者之和会大于墙钟。
type taskUsage struct {
	Tokens          tokenUsage `json:"tokens"`
	LLMInvocations  int        `json:"llm_calls"`
	LLMLatencyMs    int64      `json:"llm_latency_ms"`
	ToolInvocations int        `json:"tool_calls"`
	ToolDurationMs  int64      `json:"tool_duration_ms"`
	ElapsedMs       int64      `json:"elapsed_ms"`
}

type tokenUsage struct {
	In     int64 `json:"in"`
	Out    int64 `json:"out"`
	Cached int64 `json:"cached"`
}

// getTaskUsageHandler 处理 GET /tasks/:id/usage：合计任务名下的 LLM 与工具调用，权威口径
// 直接 SUM 调用记录（前端按事件累加会漏掉没有文字的纯工具调用）。
func getTaskUsageHandler(tasks TaskReader, llm LLMUsageAggregator, tools ToolUsageAggregator) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, taskID := c.Request.Context(), c.Param("id")
		t, err := tasks.GetByID(ctx, taskID)
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		// 落盘失败不阻断：合计少算缓冲里的几条，下次刷新补上
		if err := llm.Flush(ctx); err != nil {
			apiLog.Warn().Err(err).Str("task_id", taskID).Msg("LLM 调用记录落盘失败，用量暂按已落库的合计")
		}
		la, err := llm.AggregateByTask(ctx, taskID, llminvocation.ListFilter{})
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		ta, err := tools.AggregateByTask(ctx, taskID)
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, taskUsage{
			Tokens:          tokenUsage{In: la.InTokens, Out: la.OutTokens, Cached: la.CachedTokens},
			LLMInvocations:  la.Calls,
			LLMLatencyMs:    la.LatencyMs,
			ToolInvocations: ta.Calls,
			ToolDurationMs:  ta.DurationMs,
			ElapsedMs:       t.Elapsed(time.Now()).Milliseconds(),
		})
	}
}
