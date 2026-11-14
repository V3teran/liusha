// Package explorationgraph 实现统一的探索图：目标 → 动作 → 观察 → 结果（观察经复现门坐实为结果）。
//
// 节点（与 DB 约束 ck_exploration_node_kind 一致）：
//   - objective: 根目标（来源 user，代表任务本身，随任务终态收敛）与派生目标（来源 planner，
//     结果分析提出的方向）
//   - action: planner 生成的执行动作（有 state/complexity/depends_on），统一挂在根目标下
//   - observation: executor 产出的观察（假设带 repro、有验证状态，待复现门验证 / 事实写入即终态）
//   - result: 坐实的最终结果（仅复现门产出）
//
// 写权限不变式：planner 写 objective/action；executor 写 observation；evaluator 只经复现门
// 建 result——observation(假设) 晋升为 result 的唯一入口在 evaluator。复放明细写
// exploration_verification 审计链，不建节点。
//
// 关系（与 DB 约束 ck_exploration_edge_rel 一致，见 Relation）：
//   - generates: objective → action、action → observation、observation → result（推导链）
//   - triggers: result → objective/action（结果催生新目标 / 延续动作）
//   - supersedes: objective → objective（新派生目标吸收旧派生目标的范围，src=新、dst=旧；
//     与旧目标的 superseded 状态同一事务落定，见 RevisePlan）
//
// 动作间执行依赖不走边，落在 action 的 metadata.depends_on，由 CanExecute 直接消费。
package explorationgraph

import (
	"encoding/json"
	"time"

	"github.com/V3teran/liusha/internal/framework/core"

	"strings"
)

// State 是 action 与 objective 的状态（类型别名，指向 core.NodeState）
type State = core.NodeState

// 状态常量：实体定义在 core，经类型别名提升为领域 API
const (
	StateOpen       = core.NodeStateOpen
	StateRunning    = core.NodeStateRunning
	StateCompleted  = core.NodeStateCompleted
	StateFailed     = core.NodeStateFailed
	StateExhausted  = core.NodeStateExhausted
	StateAborted    = core.NodeStateAborted
	StateSuperseded = core.NodeStateSuperseded
)

// Complexity 是 action 的复杂度（planner 提案 → executor 提示词的可读分档；
// 模型档位路由不读本值——LLM 档位由 agent.complexity 决定）。
type Complexity string

// 复杂度三档：与 planner 输出契约（simple|moderate|complex）严格一致；
// LLM 给出的同义词（trivial/medium/extreme）在 NormalizeComplexity 归一。
const (
	ComplexitySimple   Complexity = "simple"   // 简单
	ComplexityModerate Complexity = "moderate" // 中等
	ComplexityComplex  Complexity = "complex"  // 复杂
)

// VerificationStatus 是假设观察的验证状态（类型别名，指向 core.VerificationStatus）。
type VerificationStatus = core.VerificationStatus

// 验证状态常量：实体定义在 core，经别名提升为领域 API
const (
	VerificationPending   = core.VerificationPending
	VerificationConfirmed = core.VerificationConfirmed
	VerificationRefuted   = core.VerificationRefuted
)

// ObservationCategory 是观察的信息类别：一类主张加五个领域中立的信息域，决定观察的
// 生命周期，规划时按域组织事实。词汇表对任意领域的探索 agent 通用；分类定义随执行器
// 输出契约（executor/contract）固定，领域例子由执行器章程承载，换领域自定义章程即可。
//
//   - hypothesis：待验证的判断（包括一眼可见的问题）——必须携带复现配方，经复现门
//     坐实后才成为 result。没有不经验证就成立的「问题」：坏事实要么可复现（走
//     hypothesis），要么只是环境的反应（记 behavior）
//   - entity：环境中存在的单个对象及其属性（inventory：有什么）
//   - access：触达与运用对象的途径（affordance：怎么用）
//   - structure：对象之间的关系与布局（topology：怎么连）
//   - behavior：环境受激后的反应与内在机制，包括阻碍推进的防护（dynamics：怎么动）
//   - note：无法归入上述任何域的杂项（兜底；写作顺序上的最后手段）
type ObservationCategory string

// CategoryHypothesis 等是观察类别的枚举值（语义见 ObservationCategory）。
const (
	CategoryHypothesis ObservationCategory = "hypothesis"
	CategoryEntity     ObservationCategory = "entity"
	CategoryAccess     ObservationCategory = "access"
	CategoryStructure  ObservationCategory = "structure"
	CategoryBehavior   ObservationCategory = "behavior"
	CategoryNote       ObservationCategory = "note"
)

// FactCategories 是事实的信息域，按规划时呈现的顺序排列。
var FactCategories = []ObservationCategory{CategoryEntity, CategoryAccess, CategoryStructure, CategoryBehavior, CategoryNote}

// Priority 是节点的优先级（类型别名，指向 core.Priority）
type Priority = core.Priority

// 优先级常量：实体定义在 core，经别名提升为领域 API
const (
	PriorityCritical = core.PriorityCritical
	PriorityHigh     = core.PriorityHigh
	PriorityMedium   = core.PriorityMedium
	PriorityLow      = core.PriorityLow
)

// Relation 是边的关系类型（3 种，小写字面量即存储值）
type Relation string

// 探索图关系（与 DB 约束 ck_exploration_edge_rel 严格一致）：
// generates 是贯穿推导链的"产出"关系：
// objective →(generates)→ action →(generates)→ observation →(generates)→ result。
// triggers 是"发现滋生动作/新方向"的因果溯源（result → objective/action）——
// 因果链（成果A 催生后续动作进而产出成果B）沿边可回溯。
// 动作间执行依赖（DAG 调度）不走边——落在 action 节点的 metadata.depends_on，
// 由 CanExecute 直接消费（依赖集随节点原子读写，无需二次 Join）。
const (
	RelGenerates  Relation = "generates"  // 上游产出下游（推导链）
	RelTriggers   Relation = "triggers"   // result → objective/action（发现滋生动作/新方向）
	RelSupersedes Relation = "supersedes" // objective → objective（吸收：新目标接管旧目标范围）
)

// SourceType 是节点的来源类型
type SourceType string

// 节点来源：谁创建的（审计溯源）。
const (
	SourceUser      SourceType = "user"      // 用户创建
	SourcePlanner   SourceType = "planner"   // planner agent 创建
	SourceExecutor  SourceType = "executor"  // executor 创建
	SourceEvaluator SourceType = "evaluator" // evaluator 创建
)

// Node 是探索图的节点（5 种类型统一表）
type Node struct {
	ID      string          `json:"id"`
	TaskID  string          `json:"task_id"`
	Kind    core.NodeKind   `json:"kind"`
	Content json.RawMessage `json:"content"`

	// action 专用字段
	State         *State      `json:"state,omitempty"`          // open/running/completed/...
	Complexity    *Complexity `json:"complexity,omitempty"`     // trivial/simple/moderate/...
	DependsOn     []string    `json:"depends_on,omitempty"`     // 依赖的其他 action ID（低层依赖，限定作用域：同一目标方向内）
	BlockedReason *string     `json:"blocked_reason,omitempty"` // 阻塞原因

	// 假设观察专用：验证状态（pending/confirmed/refuted）。事实观察与 result 没有。
	VerificationStatus *VerificationStatus `json:"verification_status,omitempty"`

	// 通用字段
	Priority   Priority        `json:"priority"` // critical/high/medium/low
	SourceType SourceType      `json:"source_type"`
	SourceID   string          `json:"source_id"`
	Metadata   json.RawMessage `json:"metadata,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Edge 是探索图的关系边（5 种关系）
type Edge struct {
	TaskID    string    `json:"task_id"`
	SrcID     string    `json:"src_id"`
	Rel       Relation  `json:"rel"`
	DstID     string    `json:"dst_id"`
	CreatedAt time.Time `json:"created_at"`
}

// Verification 是验证记录（审计链）
type Verification struct {
	ID         string             `json:"id"`
	TaskID     string             `json:"task_id"`
	NodeID     string             `json:"node_id"` // 被验证的 observation 节点 ID
	Primitives json.RawMessage    `json:"primitives"`
	Outcome    VerificationStatus `json:"outcome"` // confirmed / refuted
	Evaluation json.RawMessage    `json:"evaluation"`
	DurationMs int64              `json:"duration_ms"`
	CreatedAt  time.Time          `json:"created_at"`
}

// IsDerivedObjective 判断节点是否是派生目标：planner 在探索中提出的方向。
// 其余 objective 都是根目标（任务入口 / 控制面按用户意图创建），代表任务
// 本身，只随任务终态收敛，不接受规划层的修订。
func (n *Node) IsDerivedObjective() bool {
	return n.Kind == core.KindObjective && n.SourceType == SourcePlanner
}

// RootObjective 从按创建序升序的目标列表中取根目标：最早创建的非派生目标
// （多 host 任务每个 host 一个根目标，取最早的那个作为动作的统一归属）。
func RootObjective(objectives []Node) (Node, bool) {
	for _, n := range objectives {
		if !n.IsDerivedObjective() {
			return n, true
		}
	}
	return Node{}, false
}

// metaAnalyzed 是 result 上的分析水位键：规划层已把该结果纳入一次结果分析。
// 与那次分析产出的规划修订同事务写入（RevisePlan），因此「已分析」与「分析
// 结论已落图」恒同真同假——跨 run（暂停后继续、崩溃重投）也不会重复分析。
const metaAnalyzed = "analyzed"

// IsAnalyzed 判断 result 是否已被规划层分析过（见 metaAnalyzed）。
func (n *Node) IsAnalyzed() bool {
	if len(n.Metadata) == 0 {
		return false
	}
	var m map[string]interface{}
	if json.Unmarshal(n.Metadata, &m) != nil {
		return false
	}
	analyzed, _ := m[metaAnalyzed].(bool)
	return analyzed
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

// IsHypothesis 判断节点是否是假设观察（带验证状态，写入时即为 pending）。
func (n *Node) IsHypothesis() bool {
	return n.Kind == core.KindObservation && n.VerificationStatus != nil
}

// IsFact 判断节点是否是事实观察（没有验证状态：写入即终态）。
func (n *Node) IsFact() bool {
	return n.Kind == core.KindObservation && n.VerificationStatus == nil
}

// HasVerificationStatus 判断节点是否是处于 status 的假设观察。
func (n *Node) HasVerificationStatus(status VerificationStatus) bool {
	return n.IsHypothesis() && *n.VerificationStatus == status
}

// RefuteReason 返回已证伪假设的证伪原因（节点 metadata 的 refute_reason；没有则为空）。
func (n *Node) RefuteReason() string {
	if len(n.Metadata) == 0 {
		return ""
	}
	var m struct {
		RefuteReason string `json:"refute_reason"`
	}
	if json.Unmarshal(n.Metadata, &m) != nil {
		return ""
	}
	return m.RefuteReason
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

// NormalizeComplexity 把 LLM 输出的复杂度词归一到 Complexity（未知值归 simple）。
// NormalizeComplexity 把 LLM 输出的复杂度词归一到三档（未知值归 moderate）。
func NormalizeComplexity(s string) Complexity {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "simple", "trivial":
		return ComplexitySimple
	case "complex", "extreme":
		return ComplexityComplex
	default:
		return ComplexityModerate
	}
}
