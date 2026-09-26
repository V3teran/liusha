package planner

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/explorationgraph"
)

// ActionMetadata 是 action 节点的 metadata 结构。
type ActionMetadata struct {
	SteeringMessages []SteeringMessage `json:"steering_messages,omitempty"`
	KilledReason     *KilledReason     `json:"killed_reason,omitempty"` // 新增
}

// SteeringMessage 是纠偏消息。
type SteeringMessage struct {
	Timestamp time.Time `json:"timestamp"`
	Source    string    `json:"source"` // "planner" | "self"
	Guidance  string    `json:"guidance"`
	Applied   bool      `json:"applied"` // 是否已应用
}

// KilledReason 是 Kill 原因。
type KilledReason struct {
	Timestamp time.Time `json:"timestamp"`
	Source    string    `json:"source"` // "planner" | "actor"
	Reason    string    `json:"reason"` // 原因
}

// executeDecisions 根据评估结果执行决策。
func (p *PlannerAgent) executeDecisions(ctx context.Context, taskID string, assessment GlobalAssessment) error {
	p.logger.Info().
		Str("strategy", assessment.Strategy).
		Int("new_actions", len(assessment.NewActions)).
		Int("to_steer", len(assessment.ActionsToSteer)).
		Int("to_kill", len(assessment.ActionsToKill)).
		Msg("executing decisions")

	// 1. Kill actions
	for _, actionID := range assessment.ActionsToKill {
		reason := fmt.Sprintf("Global assessment: %s", assessment.Reasoning)
		if err := p.Kill(ctx, actionID, reason); err != nil {
			p.logger.Error().Err(err).Str("action_id", actionID).Msg("kill failed")
		}
	}

	// 2. Steer actions
	for _, guidance := range assessment.ActionsToSteer {
		if err := p.Steer(ctx, guidance.ActionID, guidance.Directive); err != nil {
			p.logger.Error().Err(err).Str("action_id", guidance.ActionID).Msg("steer failed")
		}
	}

	// 3. Create new actions
	for _, newAction := range assessment.NewActions {
		if err := p.CreateAction(ctx, taskID, newAction); err != nil {
			p.logger.Error().Err(err).Str("goal", newAction.Goal).Msg("create action failed")
		}
	}

	return nil
}

// Kill 终止指定的 action，并记录原因。
func (p *PlannerAgent) Kill(ctx context.Context, actionID string, reason string) error {
	p.logger.Warn().
		Str("action_id", actionID).
		Str("reason", reason).
		Msg("planner killing action")

	// 1. 读取当前节点
	node, err := p.world.GetNode(ctx, actionID)
	if err != nil {
		return fmt.Errorf("get node: %w", err)
	}

	// 2. 解析 metadata
	var metadata ActionMetadata
	if len(node.Metadata) > 0 {
		if err := json.Unmarshal(node.Metadata, &metadata); err != nil {
			metadata = ActionMetadata{}
		}
	}

	// 3. 添加 kill 原因
	metadata.KilledReason = &KilledReason{
		Timestamp: time.Now(),
		Source:    "planner",
		Reason:    reason,
	}

	// 4. 更新节点 metadata
	metadataBytes, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}

	if err := p.world.UpdateNodeMetadata(ctx, actionID, metadataBytes); err != nil {
		return fmt.Errorf("update metadata: %w", err)
	}

	// 5. 更新状态为 aborted
	state := explorationgraph.StateAborted
	if err := p.world.UpdateActionStateWithReason(ctx, actionID, state, nil); err != nil {
		return fmt.Errorf("update action state: %w", err)
	}

	// 6. 发布到事件总线
	if p.eventBus != nil {
		p.eventBus.PublishActionKilled(p.taskID, actionID, reason)
		p.logger.Info().Str("action_id", actionID).Msg("published action.killed event")
	}

	return nil
}

// Steer 纠偏指定的 action。
func (p *PlannerAgent) Steer(ctx context.Context, actionID string, guidance string) error {
	p.logger.Info().
		Str("action_id", actionID).
		Str("guidance", guidance).
		Msg("planner steering action")

	// 1. 读取当前节点
	node, err := p.world.GetNode(ctx, actionID)
	if err != nil {
		return fmt.Errorf("get node: %w", err)
	}

	// 2. 解析 metadata
	var metadata ActionMetadata
	if len(node.Metadata) > 0 {
		if err := json.Unmarshal(node.Metadata, &metadata); err != nil {
			// 忽略解析错误，使用空 metadata
			metadata = ActionMetadata{}
		}
	}

	// 3. 添加 steering 消息
	metadata.SteeringMessages = append(metadata.SteeringMessages, SteeringMessage{
		Timestamp: time.Now(),
		Source:    "planner",
		Guidance:  guidance,
		Applied:   false,
	})

	// 4. 更新节点 metadata
	metadataBytes, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}

	if err := p.world.UpdateNodeMetadata(ctx, actionID, metadataBytes); err != nil {
		return fmt.Errorf("update metadata: %w", err)
	}

	// 5. 发布到事件总线
	if p.eventBus != nil {
		p.eventBus.PublishActionSteered(p.taskID, actionID, guidance)
		p.logger.Info().Str("action_id", actionID).Msg("published action.steered event")
	}

	return nil
}

// CreateAction 创建新的 action。
func (p *PlannerAgent) CreateAction(ctx context.Context, taskID string, newAction NewAction) error {
	p.logger.Info().
		Str("goal", newAction.Goal).
		Str("priority", newAction.Priority).
		Msg("creating new action")

	// 创建 action 节点
	state := explorationgraph.StateOpen
	node := explorationgraph.Node{
		ID:         uuid.New().String(),
		TaskID:     taskID,
		Kind:       core.KindAction,
		State:      &state,
		Content:    json.RawMessage(fmt.Sprintf(`"%s"`, newAction.Goal)),
		Priority:   explorationgraph.Priority(newAction.Priority),
		DependsOn:  newAction.DependsOn,
		SourceType: explorationgraph.SourcePlanner,
		SourceID:   "control-plane",
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}

	_, err := p.world.CreateNode(ctx, node)
	if err != nil {
		return fmt.Errorf("create node: %w", err)
	}

	return nil
}
