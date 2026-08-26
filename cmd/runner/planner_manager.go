package main

import (
	"context"
	"sync"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/planneragent"
)

// plannerAgentManager 管理所有活跃的 Planner Agent 生命周期
type plannerAgentManager struct {
	agents map[string]*plannerAgentInstance
	mu     sync.RWMutex
	logger zerolog.Logger
}

type plannerAgentInstance struct {
	agent  *planneragent.Agent
	cancel context.CancelFunc
}

func newPlannerAgentManager(logger zerolog.Logger) *plannerAgentManager {
	return &plannerAgentManager{
		agents: make(map[string]*plannerAgentInstance),
		logger: logger,
	}
}

// Start 为指定 TaskID 启动 Planner Agent
func (m *plannerAgentManager) Start(ctx context.Context, h handler, taskID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 检查是否已启动
	if _, exists := m.agents[taskID]; exists {
		m.logger.Debug().Str("task_id", taskID).Msg("Planner Agent 已存在，跳过启动")
		return nil
	}

	// 创建 Planner Agent
	agentCtx, cancel := context.WithCancel(ctx)
	agent := planneragent.New(planneragent.Config{
		TaskID:       taskID,
		EventBus:     h.eventBus,
		World:        h.world,
		ControlPlane: h.controlPlane,
		Router:       h.router,
		Logger:       h.logger.With().Str("component", "planner_agent").Str("task_id", taskID).Logger(),
	})

	// 记录实例
	m.agents[taskID] = &plannerAgentInstance{
		agent:  agent,
		cancel: cancel,
	}

	// 在后台启动 Planner Agent
	go func() {
		m.logger.Info().Str("task_id", taskID).Msg("Planner Agent goroutine 启动")
		if err := agent.Start(agentCtx); err != nil {
			m.logger.Error().Err(err).Str("task_id", taskID).Msg("Planner Agent 异常退出")
		}
		m.logger.Info().Str("task_id", taskID).Msg("Planner Agent goroutine 退出")
	}()

	// 发布 TaskStarted 事件，触发初始规划
	h.eventBus.PublishTaskStarted(taskID)

	m.logger.Info().Str("task_id", taskID).Msg("Planner Agent 已启动")
	return nil
}

// Stop 停止指定 TaskID 的 Planner Agent
func (m *plannerAgentManager) Stop(taskID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	instance, exists := m.agents[taskID]
	if !exists {
		return
	}

	// 取消上下文，停止 Agent
	instance.cancel()
	instance.agent.Stop()

	// 清理记录
	delete(m.agents, taskID)

	m.logger.Info().Str("task_id", taskID).Msg("Planner Agent 已停止")
}

// StopAll 停止所有 Planner Agent
func (m *plannerAgentManager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for taskID, instance := range m.agents {
		instance.cancel()
		instance.agent.Stop()
		m.logger.Info().Str("task_id", taskID).Msg("Planner Agent 已停止（批量清理）")
	}

	m.agents = make(map[string]*plannerAgentInstance)
}
