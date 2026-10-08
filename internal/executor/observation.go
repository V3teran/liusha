package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/V3teran/liusha/internal/evaluator"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
)

// createObservationNode 为观察结果创建 observation 节点
func (e *Engine) createObservationNode(
	ctx context.Context,
	taskID string,
	actionID string,
	obs Observation,
) (*explorationgraph.Node, error) {
	// 构造 content
	contentMap := map[string]interface{}{
		"statement": obs.Statement,
		"reasoning": obs.Reasoning,
		"test_plan": obs.TestPlan,
	}

	if obs.Severity != "" {
		contentMap["severity"] = obs.Severity
	}

	if len(obs.Repro) > 0 {
		contentMap["repro"] = obs.Repro
	}

	content, err := json.Marshal(contentMap)
	if err != nil {
		return nil, fmt.Errorf("marshal content: %w", err)
	}

	// 映射 confidence
	var confidence explorationgraph.Confidence
	switch obs.Confidence {
	case "low":
		confidence = "low"
	case "medium":
		confidence = "medium"
	case "high":
		confidence = "high"
	default:
		confidence = "low"
	}

	// 创建节点
	node := explorationgraph.Node{
		ID:         uuid.New().String(),
		TaskID:     taskID,
		Kind:       core.KindObservation,
		Content:    content,
		Confidence: &confidence,
		Priority:   explorationgraph.PriorityMedium,
		SourceType: explorationgraph.SourceExecutor,
		SourceID:   e.agentRunID,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}

	// 写入探索图
	id, err := e.graph.CreateNode(ctx, node)
	if err != nil {
		return nil, fmt.Errorf("create node: %w", err)
	}

	node.ID = id

	// 创建 action → observation 关系（GENERATES）
	edge := core.GraphEdge{
		From:     actionID,
		To:       id,
		Relation: string(explorationgraph.RelGenerates),
	}

	if err := e.graph.CreateEdge(ctx, &edge); err != nil {
		e.logger.Warn().
			Err(err).
			Str("action_id", actionID).
			Str("observation_id", id).
			Msg("创建边失败（不影响节点创建）")
	}

	return &node, nil
}

// observationToAttempt 将 observation 节点转换为 Attempt
func (e *Engine) observationToAttempt(
	taskID string,
	node *explorationgraph.Node,
	obs Observation,
) (evaluator.Attempt, error) {
	// 设置默认值
	severity := obs.Severity
	if severity == "" {
		severity = "medium"
	}

	confidence := obs.Confidence
	if confidence == "" {
		confidence = "low"
	}

	// 构造 Content
	content := map[string]string{
		"summary":    obs.Statement,
		"severity":   severity,
		"confidence": confidence,
	}
	contentJSON, err := json.Marshal(content)
	if err != nil {
		return evaluator.Attempt{}, fmt.Errorf("marshal content: %w", err)
	}

	return evaluator.Attempt{
		TaskID:     taskID,
		NodeID:     node.ID,
		Kind:       "result", // 晋升为 result 节点
		Primitives: obs.Repro,
		Content:    contentJSON,
		Priority:   "medium",
	}, nil
}
