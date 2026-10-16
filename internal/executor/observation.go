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
	"github.com/V3teran/liusha/internal/tools"
)

// createObservationNode 为观察结果创建 observation 节点。
//
// 图级 Confidence 是验证生命周期裁决（复现门独占写权）：带 repro 的假设
// 以 unverified 入图等待裁决；无 repro 的执行记录/证据不是假设，不参与
// 该生命周期，不携带 Confidence。executor 的自评置信度（low/medium/high）
// 是假设载荷的一部分，随 Content 落库。
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

	if obs.Confidence != "" {
		contentMap["confidence"] = obs.Confidence
	}
	if obs.Severity != "" {
		contentMap["severity"] = obs.Severity
	}

	// 保存 ExecutionTrace（治本方案的核心）
	if obs.ExecutionTrace != nil {
		contentMap["execution_trace"] = obs.ExecutionTrace
	}

	if len(obs.Repro) > 0 {
		// 复现配方归一化：引擎收割路径落图前的统一归一化出口——形状漂移（标量断言、assert 误放 recipe 等）在此统一矫正，
		// 复现门只认信封。矫正失败保留原始配方（门侧会以 invalid_recipe 终止重试，
		// 而不是在这里静默丢观察）。
		normalized, _, nErr := tools.NormalizeReproEnvelope(obs.Repro)
		if nErr == nil {
			obs.Repro = normalized
		}
		contentMap["repro"] = obs.Repro
	}

	content, err := json.Marshal(contentMap)
	if err != nil {
		return nil, fmt.Errorf("marshal content: %w", err)
	}

	node := explorationgraph.Node{
		ID:         uuid.New().String(),
		TaskID:     taskID,
		Kind:       core.KindObservation,
		Content:    content,
		Priority:   explorationgraph.PriorityMedium,
		SourceType: explorationgraph.SourceExecutor,
		SourceID:   e.agentRunID,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	if len(obs.Repro) > 0 {
		unverified := explorationgraph.ConfidenceUnverified
		node.Confidence = &unverified
	}

	// action → observation 关系边与节点原子落库（边失败即整体失败，
	// 不产生失去归属的孤立观察）
	if actionID != "" {
		edge := explorationgraph.Edge{
			TaskID:    taskID,
			SrcID:     actionID,
			Rel:       explorationgraph.RelGenerates,
			DstID:     node.ID,
			CreatedAt: time.Now(),
		}
		if err := e.graph.CreateNodeWithEdges(ctx, node, []explorationgraph.Edge{edge}); err != nil {
			return nil, fmt.Errorf("create node with edges: %w", err)
		}
		return &node, nil
	}

	if _, err := e.graph.CreateNode(ctx, node); err != nil {
		return nil, fmt.Errorf("create node: %w", err)
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
