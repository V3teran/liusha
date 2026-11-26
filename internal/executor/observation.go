package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
)

// createObservationNode 为观察结果创建 observation 节点。
//
// 字段按类别结构化约束（契约的唯一执行点，不依赖模型自觉）：
//   - 所有观察：statement / category / reasoning；
//   - hypothesis（待验证主张）专属：repro（必带）、impact、suggestion、priority、
//     验证状态 pending —— 进复现门验证；
//   - 其余五类事实：写入即终态（可信来自产出它的动作），只带上面三项，
//     不带任何假设专属字段。模型若给事实误填这些字段，在此一并丢弃。
func (e *Engine) createObservationNode(
	ctx context.Context,
	taskID string,
	actionID string,
	obs Observation,
) (*explorationgraph.Node, error) {
	contentMap := map[string]interface{}{
		"statement": obs.Statement,
		"category":  obs.Category,
		"reasoning": obs.Reasoning,
	}

	node := explorationgraph.Node{
		ID:         uuid.New().String(),
		TaskID:     taskID,
		Kind:       core.KindObservation,
		SourceType: explorationgraph.SourceExecutor,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}

	switch obs.Category {
	case explorationgraph.CategoryHypothesis:
		// hypothesis 必带 repro，并携带主张专属字段与待验证状态
		if len(obs.Repro) == 0 {
			return nil, fmt.Errorf("hypothesis observation requires repro")
		}
		contentMap["repro"] = obs.Repro
		if obs.Impact != "" {
			contentMap["impact"] = obs.Impact
		}
		if obs.Suggestion != "" {
			contentMap["suggestion"] = obs.Suggestion
		}
		node.Priority = explorationgraph.NormalizePriority(obs.Priority)
		pending := explorationgraph.VerificationPending
		node.VerificationStatus = &pending

	case explorationgraph.CategoryEntity, explorationgraph.CategoryAccess, explorationgraph.CategoryStructure, explorationgraph.CategoryBehavior, explorationgraph.CategoryNote:
		// 事实只带 statement/category/reasoning —— 无 repro/impact/suggestion/priority/验证状态。
		// opens_deeper 是事实专属的线索信号：本条观察揭示了一条走得通、尚未推进到结论的
		// 路径，写进 metadata 供规划层优先深挖（见 Node.OpensDeeper）。假设本身就在复现门里
		// 推进，不带此信号。
		if obs.OpensDeeper {
			node.Metadata = explorationgraph.OpensDeeperMetadata()
		}

	default:
		return nil, fmt.Errorf("unknown observation category: %s", obs.Category)
	}

	content, err := json.Marshal(contentMap)
	if err != nil {
		return nil, fmt.Errorf("marshal content: %w", err)
	}
	node.Content = content

	// action → observation 关系边与节点原子落库
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

// ─────────────────────────────────────────────────────────────
// 观察载荷（Observation）
// ─────────────────────────────────────────────────────────────

// Observation 是执行器从环境感知到的一条断言（Category 语义见 explorationgraph.ObservationCategory）。
// 验证状态由 createObservationNode 按 Category 设置，不由执行器给出。
type Observation struct {
	// Statement：观察的陈述
	Statement string `json:"statement"`

	// Category：观察分类（决定生命周期）
	Category explorationgraph.ObservationCategory `json:"category"`

	// Reasoning：为什么做出这个观察
	Reasoning string `json:"reasoning"`

	// 以下四项是 hypothesis（待验证主张）专属，事实不带（createObservationNode 按类别约束）。

	// Priority：主张的优先级（low/medium/high/critical），与图级 Priority 同一概念。
	Priority string `json:"priority,omitempty"`

	// Impact：主张为真时的影响与波及面（中性因果陈述）。
	Impact string `json:"impact,omitempty"`

	// Suggestion：处理建议（若要改变这一局面可以怎么做）。
	Suggestion string `json:"suggestion,omitempty"`

	// Repro：复现配方 —— hypothesis 必带（无配方的推测无从验证），事实不带。
	// 形式：信封 {kind, recipe, assert}，recipe/assert 的形状由 kind 定义（http=HTTP 请求，steps=自然语言步骤）
	Repro json.RawMessage `json:"repro,omitempty"`

	// OpensDeeper 是事实观察专属的线索信号：本条观察揭示了一条走得通、但尚未推进到
	// 结论的路径。事实类写入即终态，这个信号让规划层把它单列为待深挖的线索优先处理；
	// 假设不带（它本身就在复现门里推进）。createObservationNode 只对事实类采纳。
	OpensDeeper bool `json:"opens_deeper,omitempty"`
}
