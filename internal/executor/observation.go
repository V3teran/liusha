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

// createObservationNode 为观察结果创建 observation 节点
//
// 验证状态由 Category 完全决定（不由 executor 赋值）：
// - hypothesis + repro → pending（待复现门验证）
// - 其余全部 → 没有验证状态（事实写入即终态：可信来自产出它的动作（推导链出处），无需验证）
func (e *Engine) createObservationNode(
	ctx context.Context,
	taskID string,
	actionID string,
	obs Observation,
) (*explorationgraph.Node, error) {
	// 构造 content
	contentMap := map[string]interface{}{
		"statement": obs.Statement,
		"category":  obs.Category,
		"reasoning": obs.Reasoning,
	}

	if obs.Impact != "" {
		contentMap["impact"] = obs.Impact
	}
	if obs.Suggestion != "" {
		contentMap["suggestion"] = obs.Suggestion
	}

	// 复现配方（假设必带；收割时已归一化）
	if len(obs.Repro) > 0 {
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
		Priority:   explorationgraph.NormalizePriority(obs.Priority), // 优先级只在节点层一份
		SourceType: explorationgraph.SourceExecutor,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}

	// 只有 hypothesis 进验证生命周期（pending → 复现门验证），其余五个信息域
	// 都是写入即终态的事实，不带验证状态。
	switch obs.Category {
	case explorationgraph.CategoryHypothesis:
		// hypothesis 必须有 repro
		if len(obs.Repro) == 0 {
			return nil, fmt.Errorf("hypothesis observation requires repro")
		}
		pending := explorationgraph.VerificationPending
		node.VerificationStatus = &pending

	case explorationgraph.CategoryEntity, explorationgraph.CategoryAccess, explorationgraph.CategoryStructure, explorationgraph.CategoryBehavior, explorationgraph.CategoryNote:

	default:
		return nil, fmt.Errorf("unknown observation category: %s", obs.Category)
	}

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

	// Priority：主张的优先级（low/medium/high/critical）——领域通用词，
	// 与图级 Priority 同一概念（探索资源有限，轻重缓急任何领域都有）。
	Priority string `json:"priority,omitempty"`

	// Impact：主张为真时的影响与波及面（中性因果陈述）。
	Impact string `json:"impact,omitempty"`

	// Suggestion：处理建议（若要改变这一局面可以怎么做）。
	Suggestion string `json:"suggestion,omitempty"`

	// Repro：复现配方
	//   - hypothesis：必须（无配方的推测无从验证）
	//   - 其余类别：可选
	// 形式：JSON，由域定义（web=HTTP请求序列，generic=自由形式等）
	Repro json.RawMessage `json:"repro,omitempty"`
}
