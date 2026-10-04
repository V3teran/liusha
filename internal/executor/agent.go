// Package executor 提供执行层的 Agent
//
// Agent 是持续运行的异步执行器，通过事件驱动响应 Action，生成 Observation
package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/bus"
	"github.com/V3teran/liusha/internal/constants"
	"github.com/V3teran/liusha/internal/evaluator"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/framework/runtime"
	"github.com/V3teran/liusha/internal/tools"
)

// 编译时检查接口实现
var _ core.Agent = (*Agent)(nil)

// Agent 是事件驱动的执行器 Agent
//
// 职责：
// - 订阅 ActionProposed 事件（由 PlannerAgent 发布）
// - 执行 Action 并生成 Observation（通过 Attempt）
// - 发布 ActionCompleted 事件
// - 完全异步，不阻塞任何调用方
type Agent struct {
	graph        *explorationgraph.Store
	executor     Interface
	eventBus     bus.Bus
	logger       zerolog.Logger
	taskID       string
	maxSteps     int
	completionCh chan Report // 任务完成通知通道

	// Checkpoint 系统
	checkpointer     core.Checkpointer
	checkpointPolicy runtime.CheckpointPolicy

	stopCh chan struct{}
}

// AgentConfig 配置 Agent
type AgentConfig struct {
	TaskID       string
	Graph        *explorationgraph.Store
	Executor     Interface
	EventBus     bus.Bus
	Logger       zerolog.Logger
	MaxSteps     int         // 最大执行步数（0 表示无限制）
	CompletionCh chan Report // 可选：任务完成时写入 Report

	// Checkpoint 配置（可选）
	Checkpointer     core.Checkpointer
	CheckpointPolicy runtime.CheckpointPolicy
}

// NewAgent 创建 Agent
func NewAgent(cfg AgentConfig) *Agent {
	if cfg.MaxSteps == 0 {
		cfg.MaxSteps = 1000 // 默认最大步数
	}

	return &Agent{
		graph:            cfg.Graph,
		executor:         cfg.Executor,
		eventBus:         cfg.EventBus,
		logger:           cfg.Logger.With().Str("agent", "executor").Logger(),
		taskID:           cfg.TaskID,
		maxSteps:         cfg.MaxSteps,
		completionCh:     cfg.CompletionCh,
		checkpointer:     cfg.Checkpointer,
		checkpointPolicy: cfg.CheckpointPolicy,
		stopCh:           make(chan struct{}),
	}
}

// Run 实现 core.Agent 接口
//
// 职责：
// - 监听事件总线上的 Action 状态变化
// - 执行可调度的 Action
// - 处理依赖关系（只执行依赖已满足的 Action）
// - 发布 ActionCompleted 事件
func (a *Agent) Run(ctx context.Context) error {
	a.logger.Info().Str("task_id", a.taskID).Msg("Agent 启动")

	// 订阅事件
	sub := a.eventBus.SubscribeTask(a.taskID)
	defer sub.Cancel()

	// 执行状态
	var report Report

	// 立即检查一次待执行的 Action
	if err := a.processAvailableActions(ctx, &report); err != nil {
		a.logger.Error().Err(err).Msg("初始 Action 处理失败")
	}

	// 事件循环
	ticker := time.NewTicker(constants.ToolTimeoutQuick) // 定期兜底检查
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			a.logger.Info().Str("task_id", a.taskID).Msg("Agent 停止（context 取消）")
			report.StopWhy = stopCanceled
			a.notifyCompletion(report)
			return ctx.Err()

		case <-a.stopCh:
			a.logger.Info().Str("task_id", a.taskID).Msg("Agent 停止")
			a.notifyCompletion(report)
			return nil

		case event := <-sub.Events():
			a.logger.Debug().
				Str("task_id", a.taskID).
				Str("event_type", string(event.Type)).
				Msg("收到事件")

			// 处理事件
			if err := a.handleEvent(ctx, event, &report); err != nil {
				a.logger.Error().
					Err(err).
					Str("event_type", string(event.Type)).
					Msg("事件处理失败")
			}

			// 检查停止条件
			if a.shouldStop(&report) {
				a.logger.Info().
					Str("task_id", a.taskID).
					Str("stop_why", report.StopWhy).
					Int("steps", report.Steps).
					Msg("Agent 达到停止条件")
				a.notifyCompletion(report)
				return nil
			}

		case <-ticker.C:
			// 定期兜底：检查是否有遗漏的 Action
			if err := a.processAvailableActions(ctx, &report); err != nil {
				a.logger.Error().Err(err).Msg("定期检查 Action 失败")
			}

			// 检查停止条件
			if a.shouldStop(&report) {
				a.logger.Info().
					Str("task_id", a.taskID).
					Str("stop_why", report.StopWhy).
					Int("steps", report.Steps).
					Msg("Agent 达到停止条件（定期检查）")
				a.notifyCompletion(report)
				return nil
			}
		}
	}
}

// handleEvent 处理事件
func (a *Agent) handleEvent(ctx context.Context, event bus.Event, report *Report) error {
	switch event.Type {
	case bus.EventActionProposed:
		// Planner 提议了新 Action，立即处理
		a.logger.Info().
			Str("task_id", a.taskID).
			Msg("收到 ActionProposed 事件，处理可执行 Action")
		return a.processAvailableActions(ctx, report)

	default:
		a.logger.Debug().
			Str("event_type", string(event.Type)).
			Msg("忽略事件（非 Executor 关注）")
		return nil
	}
}

// processAvailableActions 处理所有可执行的 Action
func (a *Agent) processAvailableActions(ctx context.Context, report *Report) error {
	// 获取所有 open 状态的 Action
	openActions, err := a.graph.ListOpenActions(ctx, a.taskID)
	if err != nil {
		return fmt.Errorf("list open actions: %w", err)
	}

	if len(openActions) == 0 {
		a.logger.Debug().Str("task_id", a.taskID).Msg("无待执行 Action")
		return nil
	}

	// 获取已完成的 Action ID 集合
	completed, err := a.getCompletedActionIDs(ctx)
	if err != nil {
		return fmt.Errorf("get completed actions: %w", err)
	}

	a.logger.Debug().
		Int("completed_count", len(completed)).
		Str("task_id", a.taskID).
		Msg("已完成的 Action 数量")

	// 筛选可执行的 Action（依赖已满足）
	var executable []explorationgraph.Node
	for _, action := range openActions {
		if action.CanExecute(completed) {
			executable = append(executable, action)
		}
	}

	a.logger.Info().
		Int("open_count", len(openActions)).
		Int("executable_count", len(executable)).
		Str("task_id", a.taskID).
		Msg("筛选可执行 Action")

	if len(executable) == 0 {
		a.logger.Debug().Msg("无可执行 Action（等待依赖满足）")
		return nil
	}

	// 执行所有可执行的 Action（TODO: 支持并行）
	for _, action := range executable {
		if err := ctx.Err(); err != nil {
			return err
		}

		if err := a.executeAction(ctx, action, report); err != nil {
			a.logger.Error().
				Err(err).
				Str("action_id", action.ID).
				Msg("执行 Action 失败")
			// 继续执行其他 Action（不因单个失败而中止）
		}
	}

	return nil
}

// executeAction 执行单个 Action
func (a *Agent) executeAction(
	ctx context.Context,
	action explorationgraph.Node,
	report *Report,
) error {
	a.logger.Info().
		Str("action_id", action.ID).
		Str("priority", string(action.Priority)).
		Msg("开始执行 Action")

	// 使用 CAS 标记为 running（防止并发执行同一 action）
	ok, err := a.graph.CompareAndSwapActionState(
		ctx,
		action.TaskID,
		action.ID,
		explorationgraph.StateOpen,
		explorationgraph.StateRunning,
		nil,
	)
	if err != nil {
		return fmt.Errorf("CAS to running: %w", err)
	}
	if !ok {
		// CAS 失败，说明 action 已被其他 executor 抢占
		a.logger.Info().
			Str("action_id", action.ID).
			Msg("Action 已被其他 executor 抢占，跳过")
		return nil
	}

	// 执行窗口起点：收割本 Action 期间 executor 新写的「带复现配方的观察提议」。
	windowStart := time.Now()

	// 执行 Action（调用 Interface）
	attempts, execErr := a.executor.Execute(ctx, action)

	// 更新状态
	if execErr != nil {
		errMsg := execErr.Error()
		if err := a.graph.UpdateActionStateWithReason(
			ctx,
			action.ID,
			explorationgraph.StateFailed,
			&errMsg,
		); err != nil {
			a.logger.Error().Err(err).Str("action_id", action.ID).Msg("标记失败状态失败")
		}
		return execErr
	}

	// 标记为 done
	if err := a.graph.UpdateActionStateWithReason(
		ctx,
		action.ID,
		explorationgraph.StateDone,
		nil,
	); err != nil {
		a.logger.Error().Err(err).Str("action_id", action.ID).Msg("标记完成状态失败")
	}

	// 更新 report
	report.Steps++
	report.Attempts += len(attempts)

	// 创建 Observation 节点（新增逻辑）
	if err := a.createObservation(ctx, action, attempts, execErr); err != nil {
		a.logger.Error().Err(err).Str("action_id", action.ID).Msg("创建 Observation 失败")
		// 不返回错误，继续执行流程
	}

	// 收割观察提议（新架构晋升链的入口）：write_observation(repro=...) 的在途假设
	// 转成 Attempt 交复现门——finding 只能由 evaluator 写，而没有本收割时 Attempt
	// 只源自 finding，首条 finding 无人生产，晋升链死锁。
	attempts = append(attempts, a.harvestObservationProposals(ctx, windowStart)...)

	// 先发布所有 AttemptsGenerated 事件（通知 EvaluatorAgent）
	for _, attempt := range attempts {
		a.eventBus.PublishAttemptGenerated(a.taskID, action.ID, attempt)
	}

	// 最后发布 ActionCompleted 事件（确保 Evaluator 已收到所有 Attempt）
	a.eventBus.PublishActionCompleted(a.taskID, action.ID)

	a.logger.Info().
		Str("action_id", action.ID).
		Int("attempts_count", len(attempts)).
		Msg("Action 执行完成")

	return nil
}

// harvestObservationProposals 把执行窗口内新建的、带复现配方的观察转成 Attempt。
//
// 新架构口径：晋升提议权在 executor（write_observation 带 repro），裁决权在 evaluator
// （复现门）。收割层**只认域信封、不解析域内形状**——归一化委托 tools.NormalizeReproEnvelope
// （与 write_observation 写入时同一份逻辑），Primitives = 信封整体透传，复现门按 domain 分发。
// 无配方的观察不收割——无米之炊不可复现，橡皮图章不可坐实。
func (a *Agent) harvestObservationProposals(ctx context.Context, since time.Time) []evaluator.Attempt {
	nodes, err := a.graph.ListNodesByKind(ctx, a.taskID, core.KindObservation)
	if err != nil {
		a.logger.Warn().Err(err).Str("task_id", a.taskID).Msg("收割观察提议失败")
		return nil
	}

	var attempts []evaluator.Attempt
	for _, n := range nodes {
		if n.CreatedAt.Before(since) {
			continue
		}

		var content struct {
			Statement string          `json:"statement"`
			Severity  string          `json:"severity"`
			Repro     json.RawMessage `json:"repro"`
		}
		if err := json.Unmarshal(n.Content, &content); err != nil || len(content.Repro) == 0 {
			continue
		}

		// 信封归一化（兼容历史形状；分域校验在此把关）。失败仅告警跳过——
		// write_observation 写入时已校验过，此处失败只可能是存量脏数据。
		envelope, nErr := tools.NormalizeReproEnvelope(content.Repro)
		if nErr != nil {
			a.logger.Warn().
				Err(nErr).Str("task_id", a.taskID).Str("node_id", n.ID).
				Msg("跳过无法归一化的 repro")
			continue
		}

		severity := content.Severity
		if severity == "" {
			severity = "medium"
		}
		attContent, err := json.Marshal(map[string]string{
			"summary":  content.Statement,
			"severity": severity,
			"host":     "unknown",
		})
		if err != nil {
			continue
		}

		attempts = append(attempts, evaluator.Attempt{
			TaskID:     a.taskID,
			NodeID:     n.ID,
			Kind:       core.KindResult,
			Primitives: envelope,
			Content:    attContent,
			Priority:   "medium",
		})
	}

	if len(attempts) > 0 {
		a.logger.Info().Str("task_id", a.taskID).Int("proposals", len(attempts)).
			Msg("收割到带复现配方的观察提议")
	}
	return attempts
}

// createObservation 创建 Observation 节点记录执行结果
func (a *Agent) createObservation(
	ctx context.Context,
	action explorationgraph.Node,
	attempts []evaluator.Attempt,
	execErr error,
) error {
	// 构建 Observation 内容
	content := map[string]interface{}{
		"action_id":      action.ID,
		"action_type":    extractActionType(action.Content),
		"execution_time": time.Now().Format(time.RFC3339),
		"success":        execErr == nil,
		"attempts_count": len(attempts),
	}

	if execErr != nil {
		content["error"] = execErr.Error()
		content["status"] = "failed"
	} else {
		content["status"] = "completed"
	}

	// 如果有 attempts，记录摘要信息
	if len(attempts) > 0 {
		findingSummaries := make([]string, 0, len(attempts))
		for _, att := range attempts {
			// 从 Attempt.Content 中提取摘要
			var attContent map[string]interface{}
			if err := json.Unmarshal(att.Content, &attContent); err == nil {
				if summary, ok := attContent["summary"].(string); ok {
					findingSummaries = append(findingSummaries, summary)
				}
			}
		}
		content["findings"] = findingSummaries
	}

	contentJSON, err := json.Marshal(content)
	if err != nil {
		return fmt.Errorf("marshal observation content: %w", err)
	}

	// 创建 Observation 节点
	unverified := explorationgraph.ConfidenceUnverified
	observationID := uuid.New().String()

	observation := explorationgraph.Node{
		ID:         observationID,
		TaskID:     a.taskID,
		Kind:       core.KindObservation,
		Content:    contentJSON,
		Confidence: &unverified,
		Priority:   action.Priority,
		SourceType: explorationgraph.SourceExecutor,
		SourceID:   action.ID,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}

	_, err = a.graph.CreateNode(ctx, observation)
	if err != nil {
		return fmt.Errorf("create observation node: %w", err)
	}

	// 创建边：Action → Observation
	err = a.graph.CreateEdge(ctx, &core.GraphEdge{
		From:      action.ID,
		To:        observationID,
		Relation:  string(core.RelationGenerates),
		CreatedAt: time.Now(),
	})
	if err != nil {
		a.logger.Error().Err(err).Msg("创建 Action → Observation 边失败")
		// 不返回错误，节点已创建
	}

	a.logger.Info().
		Str("observation_id", observationID).
		Str("action_id", action.ID).
		Bool("success", execErr == nil).
		Int("attempts", len(attempts)).
		Msg("Observation 节点已创建")

	return nil
}

// extractActionType 从 Action.Content 中提取类型
func extractActionType(content json.RawMessage) string {
	var data map[string]interface{}
	if err := json.Unmarshal(content, &data); err != nil {
		return "unknown"
	}
	if t, ok := data["type"].(string); ok {
		return t
	}
	return "unknown"
}

// getCompletedActionIDs 获取已完成的 Action ID 集合
func (a *Agent) getCompletedActionIDs(ctx context.Context) (map[string]bool, error) {
	completed, err := a.graph.ListCompletedActions(ctx, a.taskID)
	if err != nil {
		return nil, err
	}

	result := make(map[string]bool, len(completed))
	for _, action := range completed {
		result[action.ID] = true
	}
	return result, nil
}

// shouldStop 判断是否应停止
func (a *Agent) shouldStop(report *Report) bool {
	// 达到最大步数
	if a.maxSteps > 0 && report.Steps >= a.maxSteps {
		report.StopWhy = stopMaxSteps
		return true
	}

	// TODO: 可以添加更多停止条件
	// - 无待执行 Action 且超过一定时间无新 Action
	// - 收到外部停止信号

	return false
}

// notifyCompletion 通知任务完成
func (a *Agent) notifyCompletion(report Report) {
	if a.completionCh != nil {
		select {
		case a.completionCh <- report:
			a.logger.Info().Msg("任务完成报告已发送")
		default:
			a.logger.Warn().Msg("任务完成报告发送失败（channel 已满）")
		}
	}
}

// ============================================
// 实现 framework/core.Agent 接口
// ============================================

// Name 实现 core.Agent 接口
func (a *Agent) Name() string {
	return "executor"
}

// Stop 实现 core.Agent 接口
func (a *Agent) Stop(_ context.Context) error {
	a.logger.Info().Msg("停止 executor agent")
	close(a.stopCh)
	return nil
}
