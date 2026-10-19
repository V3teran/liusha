package main

import (
	"context"

	"github.com/V3teran/liusha/internal/cognition"
	"github.com/V3teran/liusha/internal/executor"
	"github.com/V3teran/liusha/internal/sandbox"
)

// runCognition 把 handler 依赖组装成 cognition.Deps 并驱动认知循环。
// 循环本体（四 Agent 装配/控制平面/完成检测）在 internal/cognition——
// 装配链是历史 bug 高发区，独立成包后可脱离 asynq/handler 单测。
func (h handler) runCognition(
	ctx context.Context,
	taskID, host, brief, conversationID string,
	sb sandbox.Client,
) (executor.Report, error) {
	return cognition.Run(ctx, &cognition.Deps{
		Graph:        h.graph,
		EventBus:     h.eventBus,
		ControlPlane: h.controlPlane,
		Checkpointer: h.checkpointer,
		CfgStore:     h.cfgStore,
		Router:       h.router,
		Logger:       h.logger,

		Skills:        h.skills,
		ToolsManifest: h.toolsManifest,
		Creds:         h.creds,

		ToolCalls: h.toolCalls,
		Tasks:     h.tasks,

		AgentRuns:  h.agentRuns,
		Findings:   h.findings,
		Corpus:     h.corpus,
		Embedder:   h.embedder,
		Reranker:   h.reranker,
		Insights:   h.insights,
		ProxyStore: h.proxyStore,
		AgentStore: h.agentStore,

		MaxStepsPerTask: h.runnerCfg.MaxStepsPerTask,

		Conversations:  h.conversations,
		EventPublisher: h.eventPublisher,
	}, taskID, host, brief, conversationID, sb)
}
