package httpapi

import (
	"context"
	"net/http"
	"sort"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/V3teran/liusha/internal/llminvocation"
	"github.com/V3teran/liusha/internal/toolinvocation"
)

// LLMUsageAggregator 合计某任务的 LLM 用量（*llminvocation.Store 满足）。
type LLMUsageAggregator interface {
	// Flush 把异步缓冲的调用记录落库，合计前调用，拿到的是最新口径。
	Flush(ctx context.Context) error
	AggregateAll(ctx context.Context, f llminvocation.ListFilter) (llminvocation.Aggregate, error)
	AggregateByRole(ctx context.Context, f llminvocation.ListFilter) ([]llminvocation.RoleAggregate, error)
}

// ToolUsageAggregator 合计某任务的工具用量（*toolinvocation.Store 满足）。
type ToolUsageAggregator interface {
	AggregateByTask(ctx context.Context, taskID string) (toolinvocation.Aggregate, error)
	AggregateByTaskRole(ctx context.Context, taskID string) ([]toolinvocation.RoleAggregate, error)
}

// taskUsage 是 GET /tasks/:id/usage 的响应：任务的用量合计，以及按调用方分开的一份。
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
	// ByRole 把同一批调用按调用方分开（任务详情的「智能体调用情况」）；各项之和等于上面的合计
	ByRole []roleUsage `json:"by_role"`
}

type tokenUsage struct {
	In     int64 `json:"in"`
	Out    int64 `json:"out"`
	Cached int64 `json:"cached"`
}

// roleUsage 是某一个调用方名下的用量：四个智能体，或历史压缩等不在认知循环里的调用方。
type roleUsage struct {
	Role            string     `json:"role"`
	Tokens          tokenUsage `json:"tokens"`
	LLMInvocations  int        `json:"llm_calls"`
	LLMLatencyMs    int64      `json:"llm_latency_ms"`
	ToolInvocations int        `json:"tool_calls"`
	ToolDurationMs  int64      `json:"tool_duration_ms"`
}

// mergeRoleUsage 把模型调用与工具调用两份按角色的分组合成一份：只在一侧出现的主角照列
// （监察者不调工具、历史压缩只调模型）；调用多的排前，一样多按角色名。没有调用时是空表。
func mergeRoleUsage(llm []llminvocation.RoleAggregate, tools []toolinvocation.RoleAggregate) []roleUsage {
	order := []string{}
	byRole := map[string]*roleUsage{}
	roleOf := func(name string) *roleUsage {
		u, ok := byRole[name]
		if !ok {
			u = &roleUsage{Role: name}
			byRole[name] = u
			order = append(order, name)
		}
		return u
	}
	for _, r := range llm {
		u := roleOf(r.Role)
		u.Tokens = tokenUsage{In: r.InTokens, Out: r.OutTokens, Cached: r.CachedTokens}
		u.LLMInvocations = r.Calls
		u.LLMLatencyMs = r.LatencyMs
	}
	for _, r := range tools {
		u := roleOf(r.Role)
		u.ToolInvocations = r.Calls
		u.ToolDurationMs = r.DurationMs
	}
	out := make([]roleUsage, 0, len(order))
	for _, name := range order {
		out = append(out, *byRole[name])
	}
	calls := func(u roleUsage) int { return u.LLMInvocations + u.ToolInvocations }
	sort.SliceStable(out, func(i, j int) bool {
		if calls(out[i]) != calls(out[j]) {
			return calls(out[i]) > calls(out[j])
		}
		return out[i].Role < out[j].Role
	})
	return out
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
		// 任务的智能体运行产生的调用：与模型调用按任务筛出的范围同一个口径
		filter := llminvocation.ListFilter{TaskID: taskID}
		la, err := llm.AggregateAll(ctx, filter)
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		ta, err := tools.AggregateByTask(ctx, taskID)
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		lr, err := llm.AggregateByRole(ctx, filter)
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		tr, err := tools.AggregateByTaskRole(ctx, taskID)
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
			ByRole:          mergeRoleUsage(lr, tr),
		})
	}
}
