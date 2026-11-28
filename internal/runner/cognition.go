package runner

import (
	"context"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/cognition"
	"github.com/V3teran/liusha/internal/config"
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
	limits := h.runLimits(ctx, taskLogger)
	comp, rt := limits.Compaction, limits.Runtime
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
		Runs:      h.AgentRuns,

		Findings:    h.Findings,
		Corpus:      h.Corpus,
		CorpusScope: corpusScope(tk),

		WebSearch:  h.WebSearch,
		ProxyStore: h.ProxyStore,
		AgentStore: h.AgentStore,

		MaxActionsPerTask:       h.RunnerCfg.MaxActionsPerTask,
		ActionParallelism:       h.RunnerCfg.ActionParallelism,
		VerificationParallelism: h.RunnerCfg.VerificationParallelism,
		MonitorInterval:         time.Duration(h.RunnerCfg.MonitorIntervalSeconds) * time.Second,
		StepLLMTimeout:          time.Duration(rt.StepLLMTimeoutSeconds) * time.Second,

		ContextRatios: cognition.ContextRatios{
			Planner:   comp.PlannerContextRatio,
			Executor:  comp.ExecutorContextRatio,
			Evaluator: comp.EvaluatorContextRatio,
			Monitor:   comp.MonitorContextRatio,
		},
		ToolLimits: cognition.ToolLimits{
			StepToolTimeoutSeconds: rt.StepToolTimeoutSeconds,
			ToolOutputWindowBytes:  rt.ToolOutputWindowBytes,
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

// corpusScope 是任务检索知识库的可见范围：任务场景的知识与通用知识；无场景的任务只见通用知识。
func corpusScope(tk task.Task) corpus.Scope {
	if tk.Scenario == nil {
		return corpus.Scope{}
	}
	code := tk.Scenario.Code
	return corpus.Scope{Scenario: &code}
}

// RunLimits 是一轮 run 生效的系统配置：各角色会话历史预算占比，与工具 / 模型调用的运行时旋钮。
type RunLimits struct {
	Compaction config.CompactionConfig
	Runtime    config.RuntimeConfig
}

// runLimits 任务开始时现读系统配置（多级缓存命中为主），热改即对本任务生效。读不到用户层
// （库故障）时回落到出厂值——与没有任何自定义时同一个取值，而不是各使用点另写的内置默认
// （读失败时工具输出曾因此完全不做长度钳制）。
func (h *Handler) runLimits(ctx context.Context, logger zerolog.Logger) RunLimits {
	var out RunLimits
	var err error
	if out.Compaction, err = h.Settings.Compaction(ctx); err != nil {
		logger.Warn().Err(err).Msg("读 compaction 组失败，用出厂值")
		out.Compaction = h.Settings.Factory().Compaction
	}
	if out.Runtime, err = h.Settings.Runtime(ctx); err != nil {
		logger.Warn().Err(err).Msg("读 runtime 组失败，用出厂值")
		out.Runtime = h.Settings.Factory().Runtime
	}
	return out
}
