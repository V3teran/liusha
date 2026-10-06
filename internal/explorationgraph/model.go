// Package explorationgraph 实现统一的探索图。
//
// 设计决策（2026-08-28 重构 + 2026-10-04 复现门对齐）：
//  1. 4 种节点类型在生产流转：objective/action/observation/result
//     （evaluation 已废弃——executor 侧证据归入 observation，坐实裁决归 exploration_verification）
//  2. 写权限不变式：planner 写 objective/action；executor 写 observation（假设/执行记录/证据）；
//     evaluator 只写 result——observation(假设) 经复现门晋升为 result 的唯一状态转换在 evaluator
//  3. 对标 ReAct 模式：目标 → 动作 → 观察 → 结果（观察经复现门坐实为结果）
//  4. 对标 PDDL 标准：action 是 AI Planning 标准术语
//
// 节点边界：
// - objective: 用户设定的任务目标（根目标 + result 派生的次级目标）
// - action: planner 生成的执行动作（有 state/complexity/depends_on）
// - observation: executor 产出的观察（假设带 repro / 执行记录 / 证据 content.type=evidence）
// - result: 坐实的最终结果（有 confidence=verified，仅复现门产出）
//
// 关系边界：
// - GENERATES: action → observation（动作生成观察/假设/证据）
// - CONFIRMS: evidence → observation（executor 侧证据确认假设）
// - REFUTES: evidence → observation（executor 侧证据反驳假设）
// - TRIGGERS: result → objective/action（planner：结果触发新目标/后续动作）
// - DEPENDS_ON: action → action（动作依赖动作）
// - ENABLES / BELONGS_TO: 预留语义，当前无生产者
package explorationgraph

import (
	"encoding/json"
	"time"

	"github.com/V3teran/liusha/internal/framework/core"

	"strings"
)

// State 是 action 的执行状态（类型别名，指向 core.ActionState）
type State = core.ActionState

// 状态常量别名（向后兼容）
const (
	StateOpen      = core.ActionStateOpen
	StateBlocked   = core.ActionStateBlocked
	StateRunning   = core.ActionStateRunning
	StateDone      = core.ActionStateDone
	StateFailed    = core.ActionStateFailed
	StateExhausted = core.ActionStateExhausted
	StateAborted   = core.ActionStateAborted
)

// Complexity 是 action 的复杂度（对标 PDDL 的 cost）
type Complexity string

// 复杂度分档：决定 ReAct 迭代上限与模型档位。
const (
	ComplexityTrivial  Complexity = "trivial"  // 平凡（最简单）
	ComplexitySimple   Complexity = "simple"   // 简单
	ComplexityModerate Complexity = "moderate" // 中等
	ComplexityComplex  Complexity = "complex"  // 复杂
	ComplexityExtreme  Complexity = "extreme"  // 极端（最难）
)

// Confidence 是 observation/result 的置信度（类型别名，指向 core.ObservationConfidence）
type Confidence = core.ObservationConfidence

// 置信度常量别名（向后兼容）
const (
	ConfidenceUnverified = core.ConfidenceUnverified
	ConfidenceVerified   = core.ConfidenceVerified
	ConfidenceRefuted    = core.ConfidenceRefuted
)

// Priority 是节点的优先级（类型别名，指向 core.Priority）
type Priority = core.Priority

// 优先级常量别名（向后兼容）
const (
	PriorityCritical = core.PriorityCritical
	PriorityHigh     = core.PriorityHigh
	PriorityMedium   = core.PriorityMedium
	PriorityLow      = core.PriorityLow
)

// Relation 是边的关系类型（5 种，全大写）
type Relation string

// 探索图关系：动作产出观察、评估确认/反驳、结果使能后续动作等。
const (
	RelGenerates Relation = "generates"  // action → observation（生成）
	RelConfirms  Relation = "confirms"   // evaluation → result（确认）
	RelRefutes   Relation = "refutes"    // evaluation → observation（反驳）
	RelEnables   Relation = "enables"    // result → action（使能）
	RelDependsOn Relation = "depends_on" // action → action（依赖）
)

// SourceType 是节点的来源类型
type SourceType string

// 节点来源：谁创建的（审计溯源）。
const (
	SourceUser      SourceType = "user"      // 用户创建
	SourcePlanner   SourceType = "planner"   // planner agent 创建
	SourceExecutor  SourceType = "executor"  // executor 创建
	SourceEvaluator SourceType = "evaluator" // evaluator 创建
	SourceSystem    SourceType = "system"    // 系统创建
)

// Node 是探索图的节点（5 种类型统一表）
type Node struct {
	ID      string          `json:"id"`
	TaskID  string          `json:"task_id"`
	Kind    core.NodeKind   `json:"kind"`
	Content json.RawMessage `json:"content"`

	// action 专用字段
	State         *State      `json:"state,omitempty"`          // open/running/done/...
	Complexity    *Complexity `json:"complexity,omitempty"`     // trivial/simple/moderate/...
	DependsOn     []string    `json:"depends_on,omitempty"`     // 依赖的其他 action ID（低层依赖，限定作用域：同一目标方向内）
	BlockedReason *string     `json:"blocked_reason,omitempty"` // 阻塞原因

	// observation/result 专用字段
	Confidence *Confidence `json:"confidence,omitempty"` // unverified/verified

	// 通用字段
	Priority   Priority        `json:"priority"` // critical/high/medium/low
	Owner      string          `json:"owner,omitempty"`
	SourceType SourceType      `json:"source_type"`
	SourceID   string          `json:"source_id"`
	Tags       []string        `json:"tags,omitempty"`
	Metadata   json.RawMessage `json:"metadata,omitempty"`

	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

// Edge 是探索图的关系边（5 种关系）
type Edge struct {
	TaskID    string          `json:"task_id"`
	SrcID     string          `json:"src_id"`
	Rel       Relation        `json:"rel"`
	DstID     string          `json:"dst_id"`
	Attrs     json.RawMessage `json:"attrs,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

// VerifyOutcome 是验证结果
type VerifyOutcome string

// 复现验证结论。
const (
	OutcomeConfirmed VerifyOutcome = "confirmed" // 确认
	OutcomeRefuted   VerifyOutcome = "refuted"   // 反驳
)

// Verification 是验证记录（审计链）
type Verification struct {
	ID         string          `json:"id"`
	TaskID     string          `json:"task_id"`
	NodeID     string          `json:"node_id"` // 被验证的 observation 节点 ID
	Primitives json.RawMessage `json:"primitives"`
	Outcome    VerifyOutcome   `json:"outcome"`
	Evaluation json.RawMessage `json:"evaluation"`
	DurationMs int64           `json:"duration_ms"`
	CreatedAt  time.Time       `json:"created_at"`
}

// IsAction 判断节点是否是 action
func (n *Node) IsAction() bool {
	return n.Kind == core.KindAction
}

// IsObservation 判断节点是否是 observation
func (n *Node) IsObservation() bool {
	return n.Kind == core.KindObservation
}

// IsResult 判断节点是否是 result
func (n *Node) IsResult() bool {
	return n.Kind == core.KindResult
}

// CanExecute 判断 action 是否可执行（无阻塞依赖）
func (n *Node) CanExecute(completed map[string]bool) bool {
	if !n.IsAction() {
		return false
	}
	if n.State == nil || *n.State != StateOpen {
		return false
	}
	// 检查所有依赖是否已完成
	for _, depID := range n.DependsOn {
		if !completed[depID] {
			return false
		}
	}
	return true
}

// IsVerified 判断 observation/result 是否已验证
func (n *Node) IsVerified() bool {
	if n.Confidence == nil {
		return false
	}
	return *n.Confidence == ConfidenceVerified
}

// TargetRef 是多态目标引用（域适配层使用）
type TargetRef struct {
	Domain  string `json:"domain"`   // 域标识（web/binary/cloud/lateral）
	RefKind string `json:"ref_kind"` // 引用类型（endpoint/file/instance/host）
	Locator string `json:"locator"`  // 定位符（URL/文件路径/实例 ID/IP）
}

// ObjectiveNode 是任务目标节点的简化表示（供 Monitor 等读取）
type ObjectiveNode struct {
	ID   string
	Goal string
}

// NormalizePriority 把 LLM 输出的优先级词归一到 Priority（未知值归 medium）。
// planner 提案、result 分析、控制平面注入共用本判定。
func NormalizePriority(s string) Priority {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "critical":
		return PriorityCritical
	case "high":
		return PriorityHigh
	case "low":
		return PriorityLow
	default:
		return PriorityMedium
	}
}

// PriorityFromSeverity 把漏洞 severity 映射到优先级（info 归 low，未知归 medium）。
func PriorityFromSeverity(severity string) Priority {
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case "critical":
		return PriorityCritical
	case "high":
		return PriorityHigh
	case "low", "info":
		return PriorityLow
	default:
		return PriorityMedium
	}
}

// NormalizeComplexity 把 LLM 输出的复杂度词归一到 Complexity（未知值归 simple）。
func NormalizeComplexity(s string) Complexity {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "moderate":
		return ComplexityModerate
	case "complex":
		return ComplexityComplex
	case "trivial":
		return ComplexityTrivial
	case "extreme":
		return ComplexityExtreme
	default:
		return ComplexitySimple
	}
}
