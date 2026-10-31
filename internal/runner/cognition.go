package runner

import (
	"context"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/cognition"
	"github.com/V3teran/liusha/internal/executor"
	"github.com/V3teran/liusha/internal/sandbox"
)

// runCognition 把 handler 依赖组装成 cognition.Deps 并驱动认知循环。
// 循环本体（四 Agent 装配/控制平面/完成检测）在 internal/cognition——
// 装配链是历史 bug 高发区，独立成包后可脱离 asynq/handler 单测。
func (h *Handler) runCognition(
	ctx context.Context,
	taskLogger zerolog.Logger,
	taskID, host, brief, conversationID string,
	sb sandbox.Client,
) (executor.Report, error) {
	// 系统配置任务开始时现读（多级缓存命中为主）：滚动窗口 / 工具钳制 / 尾部保留
	// 热改即对本任务生效。读失败用零值——下游各使用点回退内置默认，不阻塞任务。
	comp, compErr := h.Settings.Compaction(ctx)
	if compErr != nil {
		taskLogger.Warn().Err(compErr).Msg("读 compaction 配置失败，滚动窗口用内置默认")
	}
	rt, rtErr := h.Settings.Runtime(ctx)
	if rtErr != nil {
		taskLogger.Warn().Err(rtErr).Msg("读 runtime 配置失败，工具限制用内置默认")
	}
	return cognition.Run(ctx, &cognition.Deps{
		Graph:        h.Graph,
		EventBus:     h.EventBus,
		ControlPlane: h.ControlPlane,
		Checkpointer: h.Checkpointer,
		CfgStore:     h.CfgStore,
		Router:       h.Router,
		Logger:       taskLogger,

		Skills:        h.Skills,
		ToolsManifest: h.ToolsManifest,
		Creds:         h.Creds,

		ToolCalls: h.ToolCalls,
		Tasks:     h.Tasks,

		AgentRuns:  h.AgentRuns,
		Findings:   h.Findings,
		Corpus:     h.Corpus,
		Embedder:   h.Embedder,
		Reranker:   h.Reranker,
		Insights:   h.Insights,
		ProxyStore: h.ProxyStore,
		AgentStore: h.AgentStore,

		MaxStepsPerTask: h.RunnerCfg.MaxStepsPerTask,

		Windows: cognition.MessageWindows{
			Planner:   comp.PlannerWindowMessages,
			Executor:  comp.ExecutorWindowMessages,
			Evaluator: comp.EvaluatorWindowMessages,
			Monitor:   comp.MonitorWindowMessages,
		},
		ToolLimits: cognition.ToolLimits{
			StepToolTimeoutSeconds: rt.StepToolTimeoutSeconds,
			RunTailBytes:           rt.RunTailBytes,
		},

		Conversations:  h.Conversations,
		EventPublisher: h.EventPublisher,
	}, taskID, host, brief, conversationID, sb)
}
