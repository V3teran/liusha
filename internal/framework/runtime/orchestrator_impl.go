package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"github.com/V3teran/liusha/internal/framework/core"
)

// OrchestratorImpl 是 Orchestrator 的默认实现。
type OrchestratorImpl struct {
	config OrchestratorConfig

	// 状态
	mu           sync.RWMutex
	state        string // idle, running, paused, completed, failed, canceled
	currentPhase string
	startTime    time.Time
	agentStates  map[string]string

	// 组件
	stateManager core.StateManager[any]
	checkpointer core.Checkpointer
	eventBus     core.EventBus
	strategy     core.CheckpointStrategy
	hooks        core.AgentHooks
	logger       zerolog.Logger

	// 控制
	ctx    context.Context
	cancel context.CancelFunc
}

// NewOrchestrator 创建 Orchestrator 实例。
func NewOrchestrator(config OrchestratorConfig, logger zerolog.Logger) (*OrchestratorImpl, error) {
	// 验证配置
	if config.TaskID == "" {
		return nil, fmt.Errorf("task_id is required")
	}
	if len(config.Agents) == 0 {
		return nil, fmt.Errorf("at least one agent is required")
	}
	if config.StateManager == nil {
		return nil, fmt.Errorf("state_manager is required")
	}
	if config.Checkpointer == nil {
		return nil, fmt.Errorf("checkpointer is required")
	}

	// 默认值
	if config.MaxConcurrentAgents == 0 {
		config.MaxConcurrentAgents = 3
	}
	if config.CheckpointStrategy == nil {
		config.CheckpointStrategy = &core.PhaseCheckpointStrategy{}
	}

	return &OrchestratorImpl{
		config:       config,
		state:        "idle",
		agentStates:  make(map[string]string),
		stateManager: config.StateManager,
		checkpointer: config.Checkpointer,
		eventBus:     config.EventBus,
		strategy:     config.CheckpointStrategy,
		hooks:        config.AgentHooks,
		logger:       logger.With().Str("component", "orchestrator").Str("task_id", config.TaskID).Logger(),
	}, nil
}

// Run 运行任务（阻塞直到完成）。
func (o *OrchestratorImpl) Run(ctx context.Context) error {
	o.mu.Lock()
	if o.state != "idle" {
		o.mu.Unlock()
		return fmt.Errorf("orchestrator is already %s", o.state)
	}
	o.state = "running"
	o.startTime = time.Now()
	o.ctx, o.cancel = context.WithCancel(ctx)
	o.mu.Unlock()

	defer func() {
		o.mu.Lock()
		if o.state == "running" {
			o.state = "completed"
		}
		o.mu.Unlock()
	}()

	// 发布任务启动事件
	if o.eventBus != nil {
		o.publishEvent(core.EventTaskStarted, map[string]any{"task_id": o.config.TaskID})
	}

	o.logger.Info().Msg("orchestrator started")

	// 按顺序运行各 Agent
	for _, agent := range o.config.Agents {
		// 检查是否取消
		select {
		case <-o.ctx.Done():
			return o.ctx.Err()
		default:
		}

		// 更新阶段
		o.mu.Lock()
		o.currentPhase = agent.Name()
		o.agentStates[agent.Name()] = "running"
		o.mu.Unlock()

		o.logger.Info().Str("agent", agent.Name()).Msg("starting agent")

		// 发布阶段事件
		if o.eventBus != nil {
			o.publishEvent(core.EventPhaseStarted, map[string]any{
				"phase": agent.Name(),
			})
		}

		// 执行钩子
		if o.hooks != nil {
			if err := o.hooks.BeforeRun(o.ctx, agent); err != nil {
				o.logger.Error().Err(err).Str("agent", agent.Name()).Msg("before run hook failed")
				return err
			}
		}

		// 运行 Agent
		err := agent.Run(o.ctx)

		// 执行钩子
		if o.hooks != nil {
			o.hooks.AfterRun(o.ctx, agent, err)
		}

		// 更新状态
		o.mu.Lock()
		if err != nil {
			o.agentStates[agent.Name()] = "failed"
		} else {
			o.agentStates[agent.Name()] = "completed"
		}
		o.mu.Unlock()

		if err != nil {
			o.logger.Error().Err(err).Str("agent", agent.Name()).Msg("agent failed")

			// 执行错误钩子
			if o.hooks != nil {
				if hookErr := o.hooks.OnError(o.ctx, agent, err); hookErr != nil {
					o.logger.Error().Err(hookErr).Msg("error hook failed")
				}
			}

			// 发布错误事件
			if o.eventBus != nil {
				o.publishEvent(core.EventAgentError, map[string]any{
					"agent": agent.Name(),
					"error": err.Error(),
				})
			}

			o.mu.Lock()
			o.state = "failed"
			o.mu.Unlock()

			return fmt.Errorf("agent %s failed: %w", agent.Name(), err)
		}

		o.logger.Info().Str("agent", agent.Name()).Msg("agent completed")

		// 发布阶段完成事件
		if o.eventBus != nil {
			o.publishEvent(core.EventPhaseCompleted, map[string]any{
				"phase": agent.Name(),
			})
		}

		// 检查是否需要 checkpoint
		elapsed := time.Since(o.startTime)
		if o.strategy.ShouldSave(o.ctx, agent.Name(), elapsed) {
			checkpointID, err := o.saveCheckpoint(o.ctx, agent.Name()+"_completed")
			if err != nil {
				o.logger.Warn().Err(err).Msg("checkpoint failed")
			} else {
				o.logger.Info().Str("checkpoint_id", string(checkpointID)).Msg("checkpoint saved")
			}
		}
	}

	// 发布任务完成事件
	if o.eventBus != nil {
		o.publishEvent(core.EventTaskCompleted, map[string]any{
			"task_id":     o.config.TaskID,
			"duration_ms": time.Since(o.startTime).Milliseconds(),
		})
	}

	o.logger.Info().Dur("duration", time.Since(o.startTime)).Msg("orchestrator completed")
	return nil
}

// Resume 从 checkpoint 恢复任务。
func (o *OrchestratorImpl) Resume(ctx context.Context, checkpointID core.CheckpointID) error {
	o.logger.Info().Str("checkpoint_id", string(checkpointID)).Msg("resuming from checkpoint")

	// 加载 checkpoint
	checkpoint, err := o.checkpointer.Load(ctx, checkpointID)
	if err != nil {
		return fmt.Errorf("load checkpoint: %w", err)
	}

	// 恢复任务状态
	if err := o.stateManager.(interface {
		LoadFromSnapshot(ctx context.Context, taskID string, snapshot json.RawMessage) error
	}).LoadFromSnapshot(ctx, o.config.TaskID, checkpoint.StateSnapshot); err != nil {
		return fmt.Errorf("restore state: %w", err)
	}

	// 恢复各 Agent 状态
	for _, agent := range o.config.Agents {
		if recoverable, ok := agent.(core.Recoverable); ok {
			if componentState, exists := checkpoint.ComponentStates[agent.Name()]; exists {
				if err := recoverable.ImportState(componentState); err != nil {
					return fmt.Errorf("restore agent %s: %w", agent.Name(), err)
				}
				o.logger.Info().Str("agent", agent.Name()).Msg("agent state restored")
			}
		}
	}

	// 恢复运行状态
	o.mu.Lock()
	o.currentPhase = checkpoint.Phase
	o.state = "running"
	o.startTime = time.Now()
	o.ctx, o.cancel = context.WithCancel(ctx)
	o.mu.Unlock()

	// 发布恢复事件
	if o.eventBus != nil {
		o.publishEvent(core.EventCheckpointLoaded, map[string]any{
			"checkpoint_id": checkpointID,
			"phase":         checkpoint.Phase,
		})
	}

	o.logger.Info().Str("phase", checkpoint.Phase).Msg("resumed from checkpoint")

	// 从当前阶段继续执行
	return o.resumeFromPhase(ctx, checkpoint.Phase)
}

// resumeFromPhase 从指定阶段继续执行。
func (o *OrchestratorImpl) resumeFromPhase(ctx context.Context, phase string) error {
	// 找到阶段对应的 Agent 索引
	startIndex := 0
	for i, agent := range o.config.Agents {
		if agent.Name() == phase {
			startIndex = i
			break
		}
	}

	// 从该 Agent 开始执行
	tempConfig := o.config
	tempConfig.Agents = o.config.Agents[startIndex:]

	tempOrch, err := NewOrchestrator(tempConfig, o.logger)
	if err != nil {
		return err
	}

	// 复制状态
	tempOrch.currentPhase = o.currentPhase
	tempOrch.agentStates = o.agentStates

	return tempOrch.Run(ctx)
}

// Pause 暂停任务（保存 checkpoint）。
func (o *OrchestratorImpl) Pause(ctx context.Context) (core.CheckpointID, error) {
	o.mu.Lock()
	if o.state != "running" {
		o.mu.Unlock()
		return "", fmt.Errorf("orchestrator is not running")
	}
	o.state = "paused"
	o.mu.Unlock()

	// 保存 checkpoint
	checkpointID, err := o.saveCheckpoint(ctx, "paused")
	if err != nil {
		return "", err
	}

	// 取消执行
	if o.cancel != nil {
		o.cancel()
	}

	o.logger.Info().Str("checkpoint_id", string(checkpointID)).Msg("orchestrator paused")
	return checkpointID, nil
}

// Cancel 取消任务。
func (o *OrchestratorImpl) Cancel(ctx context.Context) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.state != "running" && o.state != "paused" {
		return fmt.Errorf("orchestrator is not running or paused")
	}

	o.state = "canceled"

	// 取消执行
	if o.cancel != nil {
		o.cancel()
	}

	// 发布取消事件
	if o.eventBus != nil {
		o.publishEvent(core.EventTaskCanceled, map[string]any{
			"task_id": o.config.TaskID,
		})
	}

	o.logger.Info().Msg("orchestrator canceled")
	return nil
}

// Status 获取任务状态。
func (o *OrchestratorImpl) Status() OrchestratorStatus {
	o.mu.RLock()
	defer o.mu.RUnlock()

	elapsed := int64(0)
	if !o.startTime.IsZero() {
		elapsed = time.Since(o.startTime).Milliseconds()
	}

	// 计算进度
	progress := 0
	if len(o.config.Agents) > 0 {
		completedCount := 0
		for _, state := range o.agentStates {
			if state == "completed" {
				completedCount++
			}
		}
		progress = completedCount * 100 / len(o.config.Agents)
	}

	return OrchestratorStatus{
		TaskID:      o.config.TaskID,
		State:       o.state,
		Phase:       o.currentPhase,
		ElapsedMs:   elapsed,
		Progress:    progress,
		AgentStates: o.agentStates,
		UpdatedAt:   time.Now().UnixMilli(),
	}
}

// saveCheckpoint 保存当前状态为 checkpoint。
func (o *OrchestratorImpl) saveCheckpoint(ctx context.Context, phase string) (core.CheckpointID, error) {
	// 获取任务状态
	state, err := o.stateManager.Get(ctx, o.config.TaskID)
	if err != nil {
		return "", fmt.Errorf("get state: %w", err)
	}

	// 生成快照
	snapshot, err := state.Snapshot()
	if err != nil {
		return "", fmt.Errorf("create snapshot: %w", err)
	}

	// 导出各 Agent 状态
	componentStates := make(map[string]json.RawMessage)
	for _, agent := range o.config.Agents {
		if recoverable, ok := agent.(core.Recoverable); ok {
			agentState, err := recoverable.ExportState()
			if err != nil {
				o.logger.Warn().Err(err).Str("agent", agent.Name()).Msg("export agent state failed")
				continue
			}
			componentStates[agent.Name()] = agentState
		}
	}

	// 创建 checkpoint
	checkpoint := core.Checkpoint{
		TaskID:          o.config.TaskID,
		StateSnapshot:   snapshot,
		Phase:           phase,
		ComponentStates: componentStates,
		Labels: map[string]string{
			"state": o.state,
		},
	}

	// 保存
	checkpointID, err := o.checkpointer.Save(ctx, checkpoint)
	if err != nil {
		return "", fmt.Errorf("save checkpoint: %w", err)
	}

	// 发布事件
	if o.eventBus != nil {
		o.publishEvent(core.EventCheckpointCreated, map[string]any{
			"checkpoint_id": checkpointID,
			"phase":         phase,
		})
	}

	return checkpointID, nil
}

// publishEvent 发布事件。
func (o *OrchestratorImpl) publishEvent(eventType core.EventType, data map[string]any) {
	event := core.NewEvent(eventType, o.config.TaskID, "orchestrator", data)
	if err := o.eventBus.Publish(context.Background(), event); err != nil {
		o.logger.Warn().Err(err).Str("event_type", string(eventType)).Msg("publish event failed")
	}
}
