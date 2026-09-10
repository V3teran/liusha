// Orchestrator 模式的任务执行入口
package main

import (
	"context"
	"fmt"

	"github.com/V3teran/liusha/internal/eventbus"
	"github.com/V3teran/liusha/internal/executor"
	"github.com/V3teran/liusha/internal/monitor"
	"github.com/V3teran/liusha/internal/orchestrator"
	"github.com/V3teran/liusha/internal/planner"
	"github.com/V3teran/liusha/internal/provider"
	"github.com/V3teran/liusha/internal/registry"
	"github.com/V3teran/liusha/internal/tools"
	"github.com/V3teran/liusha/internal/evaluator"
	"github.com/V3teran/liusha/internal/knowledgegraph"
)

// runWithOrchestrator 使用 Orchestrator 模式执行任务
//
// 这是新架构的入口，替代旧的 executor.Loop 模式。
//
// 架构：
// - Orchestrator：协调所有 Agents
// - Planner：纯规划（生成 Actions）
// - Monitor：独立监察（定期评估）
// - Executor Pool：执行 Actions（按需调用）
// - Verifier：验证 hypotheses（按需调用）
func (h handler) runWithOrchestrator(ctx context.Context, agentID, taskID, virtualHost string) error {
	h.logger.Info().
		Str("task_id", taskID).
		Str("agent_id", agentID).
		Msg("starting task with orchestrator (new architecture)")

	// 创建事件总线
	actionBus := eventbus.New()
	plannerEventBus := executor.NewPlannerEventBus(ctx)

	// 构建 Planner 配置
	plannerConfig := planner.Config{
		TaskID:       taskID,
		EventBus:     plannerEventBus,
		ActionBus:    actionBus,
		World:        h.world,
		ControlPlane: h.controlPlane,
		Router:       h.router,
		Logger:       h.logger,
	}

	// 获取 Provider
	complexProvider, err := h.router.For(ctx, provider.ComplexityComplex)
	if err != nil {
		return fmt.Errorf("failed to get complex provider: %w", err)
	}
	simpleProvider, err2 := h.router.For(ctx, provider.ComplexitySimple)
	if err2 != nil {
		return fmt.Errorf("failed to get simple provider: %w", err2)
	}

	// 构建 Monitor 配置
	monitorConfig := monitor.Config{
		TaskID:   taskID,
		World:    h.world,
		EventBus: actionBus,
		Provider: complexProvider, // 使用 complex 模型做评估
		Interval: 0,               // 使用默认 6 分钟
		Logger:   h.logger,
	}

	// 构建 Executor 配置
	// AgentFunc: 执行单个 action 的函数（委托给现有的执行逻辑）
	agentFunc := func(ctx context.Context, action knowledgegraph.Node) error {
		// TODO: 实现真正的 agent 执行逻辑
		// 这里暂时只是占位，实际应该调用 LLM agent
		h.logger.Info().
			Str("action_id", action.ID).
			Str("kind", string(action.Kind)).
			Msg("executing action via AgentFunc")
		return nil
	}

	executorConfig := executor.Config{
		TaskID:       taskID,
		Host:         virtualHost,
		World:        h.world,
		TrafficStore: h.agentStore,
		ProxyStore:   h.proxyStore,
		FindingStore: h.findings,
		ControlPlane: h.controlPlane,
		Router:       h.router,
		Registry:     h.buildExecutorRegistry(agentID, taskID, virtualHost),
		AgentFunc:    agentFunc,
		Logger:       h.logger,
	}

	// 构建 Verifier 配置
	verifierConfig := evaluator.Config{
		World:        h.world,
		Traffic:      h.agentStore,
		FindingStore: h.findings,
		Provider:     simpleProvider, // 使用 simple 模型做验证
		Logger:       h.logger,
	}

	// 创建 Orchestrator
	orch := orchestrator.New(orchestrator.Config{
		TaskID:           taskID,
		World:            h.world,
		Traffic:          h.agentStore,
		Findings:         h.findings,
		EventBus:         actionBus,
		PlannerConfig:    plannerConfig,
		MonitorConfig:    monitorConfig,
		ExecutorConfig:   executorConfig,
		EvaluatorConfig:   verifierConfig,
		ExecutorPoolSize: 10,
		Logger:           h.logger,
	})

	// 运行 Orchestrator
	err = orch.Run(ctx)
	if err != nil {
		h.logger.Error().Err(err).Str("task_id", taskID).Msg("orchestrator failed")
		return fmt.Errorf("orchestrator run failed: %w", err)
	}

	h.logger.Info().Str("task_id", taskID).Msg("task completed successfully")
	return nil
}

// buildExecutorRegistry 构建 Executor 的工具注册表
//
// 这里注册所有 Executor 可用的工具。
func (h handler) buildExecutorRegistry(agentID, taskID, virtualHost string) *registry.Registry {
	reg := registry.New()

	// 注册所有 Executor 工具（从现有的 tools 包）
	tools.RegisterAll(reg, tools.Deps{
		TaskID:        taskID,
		AgentID:       agentID,
		Host:          virtualHost,
		Tasks:         h.tasks,
		Findings:      h.findings,
		Corpus:        h.corpus,
		Embedder:      h.embedder,
		Reranker:      h.reranker,
		Leads:         h.leads,
		ProxyStore:    h.proxyStore,
		AgentStore:    h.agentStore,
		Creds:         h.creds,
		Sandbox:       nil, // TODO: 从上下文获取 sandbox
		ToolingLoader: h.toolingLoader,
		VulnLoader:    h.vulnLoader,
	})

	// 添加工具调用记录拦截器
	reg.AddInterceptor(h.toolRecordInterceptor(agentID, taskID))

	return reg
}

