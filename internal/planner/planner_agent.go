// Package planner 提供规划层（PlannerAgent），负责根据知识图谱节点生成可执行的 Action
package planner

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/bus"
	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/knowledgegraph"
)

// PlannerAgent 是异步规划 Agent
//
// 职责：
// - 监听 EventVerificationPassed/Refuted 事件
// - 根据知识图谱生成新的 Action
// - 发布 EventActionProposed 事件
type PlannerAgent struct {
	taskID       string
	world        *knowledgegraph.Store
	planner      Planner
	eventBus     bus.Bus
	logger       zerolog.Logger
	pollInterval time.Duration // 轮询间隔（作为兜底）

	stopCh chan struct{}
}

// PlannerAgentConfig 配置
type PlannerAgentConfig struct {
	TaskID       string
	World        *knowledgegraph.Store
	Planner      Planner
	EventBus     bus.Bus
	Logger       zerolog.Logger
	PollInterval time.Duration // 默认 10s
}

// NewPlannerAgent 创建 PlannerAgent
func NewPlannerAgent(cfg PlannerAgentConfig) *PlannerAgent {
	if cfg.PollInterval == 0 {
		cfg.PollInterval = 10 * time.Second
	}

	return &PlannerAgent{
		taskID:       cfg.TaskID,
		world:        cfg.World,
		planner:      cfg.Planner,
		eventBus:     cfg.EventBus,
		logger:       cfg.Logger.With().Str("agent", "planner").Logger(),
		pollInterval: cfg.PollInterval,
		stopCh:       make(chan struct{}),
	}
}

// Start 启动 PlannerAgent（异步运行）
// 监听事件并生成新的 Action
func (a *PlannerAgent) Start(ctx context.Context) error {
	a.logger.Info().Str("task_id", a.taskID).Msg("PlannerAgent 启动")

	// 订阅事件
	events := a.eventBus.SubscribeTask(a.taskID)
	defer a.eventBus.UnsubscribeTask(a.taskID)

	// 定期检查（兜底机制）
	ticker := time.NewTicker(a.pollInterval)
	defer ticker.Stop()

	// 启动时规划一次
	if err := a.planActions(ctx); err != nil {
		a.logger.Error().Err(err).Msg("初始规划失败")
	}

	for {
		select {
		case <-ctx.Done():
			a.logger.Info().Str("task_id", a.taskID).Msg("PlannerAgent 停止（context done）")
			return ctx.Err()

		case <-a.stopCh:
			a.logger.Info().Str("task_id", a.taskID).Msg("PlannerAgent 停止")
			return nil

		case event := <-events:
			// 处理验证结果事件
			if err := a.handleEvent(ctx, event); err != nil {
				a.logger.Error().
					Err(err).
					Str("event_type", string(event.Type)).
					Msg("事件处理失败")
			}

		case <-ticker.C:
			// 定期兜底：检查是否需要新的规划
			if err := a.planActions(ctx); err != nil {
				a.logger.Error().Err(err).Msg("定期规划失败")
			}
		}
	}
}

// handleEvent 处理事件
func (a *PlannerAgent) handleEvent(ctx context.Context, event bus.Event) error {
	switch event.Type {
	case bus.EventVerificationPassed:
		// 验证通过，可能需要新的规划
		a.logger.Info().
			Str("task_id", a.taskID).
			Msg("收到 VerificationPassed 事件，触发新规划")
		return a.planActions(ctx)

	case bus.EventVerificationRefuted:
		// 证伪，需要调整规划
		a.logger.Info().
			Str("task_id", a.taskID).
			Msg("收到 VerificationRefuted 事件，触发调整规划")
		return a.planActions(ctx)

	case bus.EventTaskStarted:
		// 任务启动
		a.logger.Info().Str("task_id", a.taskID).Msg("收到 TaskStarted 事件")
		return a.planActions(ctx)

	default:
		// 忽略其他事件
		return nil
	}
}

// planActions 根据当前知识图谱生成新的 Action
func (a *PlannerAgent) planActions(ctx context.Context) error {
	a.logger.Debug().Str("task_id", a.taskID).Msg("开始规划 Action")

	// 检查是否已有可执行的 Action（而不是仅检查 open actions）
	openActions, err := a.world.ListOpenActions(ctx, a.taskID)
	if err != nil {
		return fmt.Errorf("list open actions: %w", err)
	}

	// 获取已完成的 action（用于依赖检查）
	completedNodes, err := a.world.ListNodesByKind(ctx, a.taskID, core.KindAction)
	if err != nil {
		return fmt.Errorf("list actions: %w", err)
	}

	// 过滤出已完成的 action
	var completedActions []knowledgegraph.Node
	for _, node := range completedNodes {
		if node.State != nil && *node.State == knowledgegraph.StateDone {
			completedActions = append(completedActions, node)
		}
	}

	// 构建已完成的 action ID 集合
	completed := make(map[string]bool)
	for _, action := range completedActions {
		completed[action.ID] = true
	}

	// 检查是否有可执行的 action（依赖已满足）
	var executableActions []knowledgegraph.Node
	for _, action := range openActions {
		if action.CanExecute(completed) {
			executableActions = append(executableActions, action)
		}
	}

	if len(executableActions) > 0 {
		a.logger.Debug().
			Int("open_count", len(openActions)).
			Int("executable_count", len(executableActions)).
			Msg("已有可执行 Action，跳过规划")
		return nil
	}

	// 如果有 open actions 但都不可执行（全部 blocked），记录警告
	if len(openActions) > 0 {
		a.logger.Warn().
			Int("blocked_count", len(openActions)).
			Msg("所有 open actions 都被依赖阻塞，尝试生成新规划")
	}

	// 调用 Planner 生成新的 Action（传入 taskID）
	actions, err := a.planner.Plan(ctx, a.world, a.taskID)
	if err != nil {
		return fmt.Errorf("plan: %w", err)
	}

	if len(actions) == 0 {
		a.logger.Info().Msg("Planner 未生成新 Action（可能已完成）")
		return nil
	}

	// 将新 Action 写入知识图谱
	for _, action := range actions {
		// action 已经是完整的 Node，直接写入
		if _, err := a.world.CreateNode(ctx, action); err != nil {
			a.logger.Error().
				Err(err).
				Str("action_id", action.ID).
				Msg("写入 Action 失败")
			continue
		}

		a.logger.Info().
			Str("action_id", action.ID).
			Str("priority", string(action.Priority)).
			Msg("新 Action 已生成")

		// 发布 ActionProposed 事件
		a.eventBus.PublishActionProposed(a.taskID, action.ID)
	}

	return nil
}

// Name 实现 core.Agent 接口
func (a *PlannerAgent) Name() string {
	return "planner"
}

// Run 实现 core.Agent 接口（调用 Start）
func (a *PlannerAgent) Run(ctx context.Context) error {
	return a.Start(ctx)
}

// Stop 实现 core.Agent 接口（优雅关闭）
func (a *PlannerAgent) Stop(ctx context.Context) error {
	close(a.stopCh)
	return nil
}

// ExportState 实现 core.Recoverable 接口
func (a *PlannerAgent) ExportState() (json.RawMessage, error) {
	// PlannerAgent 是无状态的，返回空对象
	return json.Marshal(map[string]interface{}{})
}

// ImportState 实现 core.Recoverable 接口
func (a *PlannerAgent) ImportState(data json.RawMessage) error {
	// PlannerAgent 是无状态的，不需要恢复
	return nil
}

// Planner 规划接口（由 planner.New 提供）
type Planner interface {
	Plan(ctx context.Context, world *knowledgegraph.Store, taskID string) ([]knowledgegraph.Node, error)
}
