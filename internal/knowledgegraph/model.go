// Package knowledgegraph 实现统一的知识图谱（认知图）。
//
// 设计决策（2026-08-28 重构 + 2026-09-XX ReAct 对齐）：
// 1. 5 种节点类型：objective/action/observation/evaluation/result
// 2. 5 种关系类型：GENERATES/CONFIRMS/REFUTES/ENABLES/DEPENDS_ON
// 3. 对标 ReAct 模式：目标 → 动作 → 观察 → 评估 → 结果
// 4. 对标 PDDL 标准：action 是 AI Planning 标准术语
//
// 节点边界：
// - objective: 用户设定的任务目标
// - action: planner 生成的执行动作（有 state/complexity/depends_on）
// - observation: executor 产出的观察结果（有 confidence）
// - evaluation: evaluator 产出的评估结论（有 outcome）
// - result: 确认的最终结果（有 confidence=verified）
//
// 关系边界：
// - GENERATES: action → observation（动作生成观察）
// - CONFIRMS: evaluation → result（评估确认结果）
// - REFUTES: evaluation → observation（评估反驳观察）
// - ENABLES: result → action（结果使能新动作）
// - DEPENDS_ON: action → action（动作依赖动作）
package knowledgegraph

import (
	"encoding/json"
	"time"
)

// NodeKind 是认知节点的类型（5 种）
type NodeKind string

const (
	KindObjective   NodeKind = "objective"   // 任务目标（用户设定）
	KindAction      NodeKind = "action"      // 执行动作（planner 生成）
	KindObservation NodeKind = "observation" // 观察结果（executor 产出）
	KindEvaluation  NodeKind = "evaluation"  // 评估结论（evaluator 产出）
	KindResult      NodeKind = "result"      // 最终结果（confirmed）
)

// State 是 action 的执行状态
type State string

const (
	StateOpen      State = "open"      // 待执行
	StateBlocked   State = "blocked"   // 被阻塞（依赖未满足）
	StateRunning   State = "running"   // 执行中
	StateDone      State = "done"      // 已完成
	StateFailed    State = "failed"    // 执行失败
	StateExhausted State = "exhausted" // 已耗尽（尝试次数用完）
	StateAborted   State = "aborted"   // 被中止
)

// Complexity 是 action 的复杂度（对标 PDDL 的 cost）
type Complexity string

const (
	ComplexityTrivial  Complexity = "trivial"  // 平凡（最简单）
	ComplexitySimple   Complexity = "simple"   // 简单
	ComplexityModerate Complexity = "moderate" // 中等
	ComplexityComplex  Complexity = "complex"  // 复杂
	ComplexityExtreme  Complexity = "extreme"  // 极端（最难）
)

// Confidence 是 observation/result 的置信度
type Confidence string

const (
	ConfidenceUnverified Confidence = "unverified" // 未验证
	ConfidenceVerified   Confidence = "verified"   // 已验证
	ConfidenceRefuted    Confidence = "refuted"    // 已证伪
)

// Priority 是节点的优先级（通用，对标 P0/P1/P2/P3）
type Priority string

const (
	PriorityCritical Priority = "critical" // 关键（P0）
	PriorityHigh     Priority = "high"     // 高（P1）
	PriorityMedium   Priority = "medium"   // 中（P2）
	PriorityLow      Priority = "low"      // 低（P3）
)

// Relation 是边的关系类型（5 种，全大写）
type Relation string

const (
	RelGenerates Relation = "GENERATES"  // action → observation（生成）
	RelConfirms  Relation = "CONFIRMS"   // evaluation → result（确认）
	RelRefutes   Relation = "REFUTES"    // evaluation → observation（反驳）
	RelEnables   Relation = "ENABLES"    // result → action（使能）
	RelDependsOn Relation = "DEPENDS_ON" // action → action（依赖）
)

// SourceType 是节点的来源类型
type SourceType string

const (
	SourceUser      SourceType = "user"      // 用户创建
	SourcePlanner   SourceType = "planner"   // planner agent 创建
	SourceExecutor  SourceType = "executor"  // executor 创建
	SourceEvaluator SourceType = "evaluator" // evaluator 创建
	SourceSystem    SourceType = "system"    // 系统创建
)

// Node 是知识图谱的节点（5 种类型统一表）
type Node struct {
	ID      string          `json:"id"`
	TaskID  string          `json:"task_id"`
	Kind    NodeKind        `json:"kind"`
	Content json.RawMessage `json:"content"`

	// action 专用字段
	State         *State      `json:"state,omitempty"`          // open/running/done/...
	Complexity    *Complexity `json:"complexity,omitempty"`     // trivial/simple/moderate/...
	DependsOn     []string    `json:"depends_on,omitempty"`     // 依赖的其他 action ID（低层依赖，限定作用域：同一 RoadmapStep 内）
	BlockedReason *string     `json:"blocked_reason,omitempty"` // 阻塞原因
	RoadmapStep   *float64    `json:"roadmap_step,omitempty"`   // 关联的 RoadmapStep 编号（如果此 Action 由 Roadmap 派发）

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

// Edge 是知识图谱的关系边（5 种关系）
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
	Evaluation   json.RawMessage `json:"evaluation"`
	DurationMs int64           `json:"duration_ms"`
	CreatedAt  time.Time       `json:"created_at"`
}

// IsAction 判断节点是否是 action
func (n *Node) IsAction() bool {
	return n.Kind == KindAction
}

// IsObservation 判断节点是否是 observation
func (n *Node) IsObservation() bool {
	return n.Kind == KindObservation
}

// IsResult 判断节点是否是 result
func (n *Node) IsResult() bool {
	return n.Kind == KindResult
}

// IsEvaluation 判断节点是否是 evaluation
func (n *Node) IsEvaluation() bool {
	return n.Kind == KindEvaluation
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

// ObjectiveNode 是任务目标节点的简化表示（用于 Monitor/Orchestrator）
type ObjectiveNode struct {
	ID   string
	Goal string
}
