package runner

import (
	"context"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/cognition"
	"github.com/V3teran/liusha/internal/sandbox"
)

// runCognition 把 handler 依赖组装成 cognition.Deps 并驱动认知循环。
// 循环本体（四 Agent 装配/任务行监视/完成检测）在 internal/cognition——
// 装配链是历史 bug 高发区，独立成包后可脱离 asynq/handler 单测。
func (h *Handler) runCognition(
	ctx context.Context,
	taskLogger zerolog.Logger,
	assignmentID, agentRunID, taskID, host, brief, conversationID string,
	installed sandbox.Inventory,
) (cognition.Report, error) {
	// 系统配置任务开始时现读（多级缓存命中为主）：压缩预算占比 / 工具钳制 /
	// 尾部保留，热改即对本任务生效。读失败用零值——下游各使用点回退内置默认，
	// 不阻塞任务。
	comp, compErr := h.Settings.Compaction(ctx)
	if compErr != nil {
		taskLogger.Warn().Err(compErr).Msg("读 compaction 配置失败，压缩预算用内置兜底")
	}
	rt, rtErr := h.Settings.Runtime(ctx)
	if rtErr != nil {
		taskLogger.Warn().Err(rtErr).Msg("读 runtime 配置失败，工具限制用内置默认")
	}
	return cognition.Run(ctx, &cognition.Deps{
		AssignmentID: assignmentID,
		AgentRunID:   agentRunID,
		Graph:        h.Graph,
		EventBus:     h.EventBus,
		CfgStore:     h.CfgStore,
		Router:       h.Router,
		Logger:       taskLogger,

		Skills:        h.Skills,
		ToolsManifest: h.ToolsManifest,
		Installed:     installed,
		Creds:         h.Creds,

		ToolCalls: h.ToolCalls,
		Tasks:     h.Tasks,

		Findings:   h.Findings,
		Corpus:     h.Corpus,
		Embedder:   h.Embedder,
		Reranker:   h.Reranker,
		ProxyStore: h.ProxyStore,
		AgentStore: h.AgentStore,

		MaxActionsPerTask: h.RunnerCfg.MaxActionsPerTask,
		StepLLMTimeout:    stepLLMTimeout(rt.StepLLMTimeoutSeconds, h.RunnerCfg.StepLLMTimeoutSeconds),

		ContextRatios: cognition.ContextRatios{
			Planner:   comp.PlannerContextRatio,
			Executor:  comp.ExecutorContextRatio,
			Evaluator: comp.EvaluatorContextRatio,
			Monitor:   comp.MonitorContextRatio,
		},
		ToolLimits: cognition.ToolLimits{
			StepToolTimeoutSeconds: rt.StepToolTimeoutSeconds,
			RunTailBytes:           rt.RunTailBytes,
			ToolOutputTailBytes:    rt.ToolOutputTailBytes,
		},
		SandboxListener: h.SandboxListener,

		Conversations:  h.Conversations,
		EventPublisher: h.EventPublisher,
	}, taskID, host, brief, conversationID, h.Sandbox)
}

// stepLLMTimeout 取单步 LLM 看门狗上限：系统配置（热改）优先，读取失败或未配置时取
// 部署配置。
func stepLLMTimeout(settingSeconds, deploySeconds int) time.Duration {
	if settingSeconds > 0 {
		return time.Duration(settingSeconds) * time.Second
	}
	return time.Duration(deploySeconds) * time.Second
}
