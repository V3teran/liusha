package runner

import (
	"context"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/cognition"
	"github.com/V3teran/liusha/internal/corpus"
	"github.com/V3teran/liusha/internal/sandbox"
	"github.com/V3teran/liusha/internal/scenario"
	"github.com/V3teran/liusha/internal/task"
)

// runCognition 把 handler 依赖组装成 cognition.Deps 并驱动认知循环。
// 循环本体（四 Agent 装配/任务行监视/完成检测）在 internal/cognition——
// 装配链是历史 bug 高发区，独立成包后可脱离 asynq/handler 单测。
func (h *Handler) runCognition(
	ctx context.Context,
	taskLogger zerolog.Logger,
	tk task.Task, agentRunID, host, conversationID string,
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
		AssignmentID: tk.AssignmentID,
		AgentRunID:   agentRunID,
		Scenario:     scenarioBlock(tk),
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

		Findings:    h.Findings,
		Corpus:      h.Corpus,
		CorpusScope: corpusScope(tk),
		ProxyStore:  h.ProxyStore,
		AgentStore:  h.AgentStore,

		MaxActionsPerTask:       h.RunnerCfg.MaxActionsPerTask,
		ActionParallelism:       h.RunnerCfg.ActionParallelism,
		VerificationParallelism: h.RunnerCfg.VerificationParallelism,
		MonitorInterval:         time.Duration(h.RunnerCfg.MonitorIntervalSeconds) * time.Second,
		StepLLMTimeout:          stepLLMTimeout(rt.StepLLMTimeoutSeconds, h.RunnerCfg.StepLLMTimeoutSeconds),

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
		RunActivity:     h.Heartbeats,
		SandboxListener: h.SandboxListener,

		Conversations:  h.Conversations,
		EventPublisher: h.EventPublisher,
	}, tk.ID, host, conversationID, h.Sandbox)
}

// scenarioBlock 按任务行的场景快照渲染场景块（不查场景表：场景之后被改、删都不影响已建任务，
// 注入路径也就没有「场景读不到」的失败分支）。
func scenarioBlock(tk task.Task) string {
	if tk.Scenario == nil {
		return ""
	}
	return scenario.Render(tk.Scenario.Name, tk.Scenario.Body)
}

// stepLLMTimeout 取单步 LLM 看门狗上限：系统配置（热改）优先，读取失败或未配置时取
// 部署配置。
func stepLLMTimeout(settingSeconds, deploySeconds int) time.Duration {
	if settingSeconds > 0 {
		return time.Duration(settingSeconds) * time.Second
	}
	return time.Duration(deploySeconds) * time.Second
}

// corpusScope 是任务检索知识库的可见范围：任务场景的知识与通用知识；无场景的任务只见通用知识。
func corpusScope(tk task.Task) corpus.Scope {
	if tk.Scenario == nil {
		return corpus.Scope{}
	}
	code := tk.Scenario.Code
	return corpus.Scope{Scenario: &code}
}
