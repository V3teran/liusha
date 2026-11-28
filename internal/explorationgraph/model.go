// Package explorationgraph 实现统一的探索图：目标 → 动作 → 观察 → 结果（观察经复现门坐实为结果）。
//
// 节点（与 DB 约束 ck_exploration_node_kind 一致）：
//   - objective: 根目标（来源 user，代表任务本身，随任务终态收敛）与派生目标（来源 planner，
//     结果分析提出的方向）
//   - action: 执行动作（有 state/complexity），挂在它所属的目标下：根目标，或规划者声明
//     它所属的进行中的派生目标（RevisePlan）
//   - observation: executor 产出的观察（假设带 repro、有验证状态，待复现门验证 / 事实写入即终态）
//   - result: 坐实的最终结果（仅复现门产出）
//
// 写权限不变式：planner 写 objective/action；executor 写 observation；evaluator 只经复现门
// 建 result——observation(假设) 晋升为 result 的唯一入口在 evaluator。复放明细写
// exploration_verification 审计链，不建节点。
//
// 关系（与 DB 约束 ck_exploration_edge_rel 一致，见 Relation）：
//   - generates: objective → action、action → observation、observation → result（推导链：
//     目标派生动作，动作产出观察，假设坐实成结果）
//   - triggers: result → objective/action（结果催生新目标 / 新动作）
//   - pursues: action → observation（动作为跟进这条线索而排出：线索进入它的执行范围）
//
// 动作之间没有执行依赖：open 即可执行，先后关系由增量规划表达——需要前一步结果的
// 动作在前一步的观察落图后才被提出，指令因此基于真实结果。
package explorationgraph

import (
	"encoding/json"
	"time"

	"github.com/V3teran/liusha/internal/framework/core"

	"strings"
)

// State 是 action 与 objective 的状态（类型别名，指向 core.NodeState）
type State = core.NodeState

// 状态常量：实体定义在 core，经类型别名提升为本包 API
const (
	StateOpen      = core.NodeStateOpen
	StateRunning   = core.NodeStateRunning
	StateCompleted = core.NodeStateCompleted
	StateFailed    = core.NodeStateFailed
	StateExhausted = core.NodeStateExhausted
	StateAborted   = core.NodeStateAborted
	StateRejected  = core.NodeStateRejected
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

// 验证状态常量：实体定义在 core，经别名提升为本包 API
const (
	VerificationPending   = core.VerificationPending
	VerificationConfirmed = core.VerificationConfirmed
	VerificationRefuted   = core.VerificationRefuted
)

// ObservationCategory 是观察的信息类别：一类主张加五类事实，决定观察的生命周期，规划时
// 按类组织事实。词汇对任何业务领域的探索通用；分类定义随执行器输出契约（executor/contract）
// 固定，领域例子写在任务的场景里。
//
//   - hypothesis：待验证的判断（包括一眼可见的问题）——必须携带复现配方，经复现门
//     坐实后才成为 result。没有不经验证就成立的「问题」：坏事实要么可复现（走
//     hypothesis），要么只是环境的反应（记 behavior）
//   - entity：环境中存在的单个对象及其属性（inventory：有什么）
//   - access：触达与运用对象的途径（affordance：怎么用）
//   - structure：对象之间的关系与布局（topology：怎么连）
//   - behavior：环境受激后的反应与内在机制，包括阻碍推进的防护（dynamics：怎么动）
//   - note：无法归入上述任何类别的杂项（兜底；写作顺序上的最后手段）
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

// FactCategories 是事实的类别，按规划时呈现的顺序排列。
var FactCategories = []ObservationCategory{CategoryEntity, CategoryAccess, CategoryStructure, CategoryBehavior, CategoryNote}

// Priority 是节点的优先级（类型别名，指向 core.Priority）
type Priority = core.Priority

// 优先级常量：实体定义在 core，经别名提升为本包 API
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
const (
	RelGenerates Relation = "generates" // 上游产出下游（推导链）
	RelTriggers  Relation = "triggers"  // result → objective/action（发现滋生动作/新方向）
	RelPursues   Relation = "pursues"   // action → observation（动作为跟进这条线索而排出）
)

// SourceType 是节点的来源类型
type SourceType string

// 节点来源：谁创建的（审计溯源）。
const (
	SourceUser      SourceType = "user"      // 用户创建
	SourcePlanner   SourceType = "planner"   // planner agent 创建
	SourceExecutor  SourceType = "executor"  // executor 创建
	SourceEvaluator SourceType = "evaluator" // evaluator 创建
	SourceMonitor   SourceType = "monitor"   // monitor（只作为否决方出现：它不建节点）
)

// Node 是探索图的节点（5 种类型统一表）
type Node struct {
	ID      string          `json:"id"`
	TaskID  string          `json:"task_id"`
	Kind    core.NodeKind   `json:"kind"`
	Content json.RawMessage `json:"content"`

	// action 专用字段
	State       *State      `json:"state,omitempty"`        // open/running/completed/...
	Complexity  *Complexity `json:"complexity,omitempty"`   // trivial/simple/moderate/...
	StateReason *string     `json:"state_reason,omitempty"` // 当前状态的原因（失败、中止、停摆）

	// Urgent 是规划者给动作标的加急（一轮至多一个）：执行者派发时它排在其它规划者排出的
	// 动作之前，仅次于用户指派的。只有规划者排出的动作可以带（库约束 ck_exploration_node_urgent）。
	Urgent bool `json:"urgent,omitempty"`

	// 假设观察专用：验证状态（pending/confirmed/refuted）。事实观察与 result 没有。
	VerificationStatus *VerificationStatus `json:"verification_status,omitempty"`

	// Lead 是事实观察带的线索（见 LeadSpec）：这条路走得通、但还没走到结论。假设观察、
	// 动作、目标与结果没有。
	Lead *LeadSpec `json:"lead,omitempty"`

	// LeadClosed 是规划层关闭这条线索时记下的类别与理由（见 LeadClosure）。nil 表示没有
	// 这条批注：线索仍开着，或由跟进它的动作的结局推导关闭（见 lead.go）。
	LeadClosed *LeadClosure `json:"lead_closed,omitempty"`

	// 通用字段
	// Priority 是主张的优先级：假设带，坐实后随结果进交付（按什么定档由任务场景规定——
	// 渗透测试里是严重度）。动作与目标没有优先级（库约束 ck_exploration_node_priority）：
	// 动作的派发先后由来源、是否加急、是否认领先索与创建时间决定（见 executor 的 dispatch），
	// 不由模型自评的分数决定。
	Priority   Priority        `json:"priority"`
	SourceType SourceType      `json:"source_type"`
	Metadata   json.RawMessage `json:"metadata,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// Version 随节点的每次改写递增（存储层维护，写入时忽略）。
	Version int64 `json:"version"`
}

// Edge 是探索图的关系边（见 Relation）
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
	Repro      json.RawMessage    `json:"repro"`
	Outcome    VerificationStatus `json:"outcome"` // confirmed / refuted
	Evidence   json.RawMessage    `json:"evidence"`
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

// meta 解析节点的 metadata（缺失或解析失败时为 nil）。
func (n *Node) meta() map[string]interface{} {
	if len(n.Metadata) == 0 {
		return nil
	}
	var m map[string]interface{}
	if json.Unmarshal(n.Metadata, &m) != nil {
		return nil
	}
	return m
}

// IsAnalyzed 判断 result 是否已被规划层分析过（见 metaAnalyzed）。
func (n *Node) IsAnalyzed() bool {
	analyzed, _ := n.meta()[metaAnalyzed].(bool)
	return analyzed
}

// LeadSpec 是一条线索的写法，三样缺一不成线索：沿着产出它的那条事实再走一步，具体做什么
// （Next）；走完这一步，哪个还没答案的问题会有答案（Question）；答案若是肯定的，能换来
// 什么（Potential）。三样都写得出，这条事实才成其为线索——写不出的只是环境里的一件事实。
// 执行者写入时把关（见 executor 的 leadSpec），这里不存模型给的价值分数：值不值得追，由
// 规划者读 Potential 判断。线索之后是否有人跟进、跟进到哪一步，不写在这里，由图上的跟进
// 关系与关闭批注推导（见 lead.go）。
type LeadSpec struct {
	Next      string `json:"next"`      // 下一步：沿这条事实具体做什么
	Question  string `json:"question"`  // 要弄清的结论
	Potential string `json:"potential"` // 推进价值：走通了能换来什么
}

// IsLead 判断节点是不是一条线索：带 Lead 的事实观察。
func (n *Node) IsLead() bool {
	return n.IsFact() && n.Lead != nil
}

// metaUrgent 是动作加急的存储键（与 Node.Urgent 同一事实）。
const metaUrgent = "urgent"

// metaLeadClosed 是线索上规划层关闭决定的存储键（与 Node.LeadClosed 同一事实）：规划层
// 判定这条线索不再跟进，值是 {"kind": …, "reason": …}。随关闭它的那次规划修订同事务写入
// （RevisePlan）；批注是一次性的，同一条线索不会被规划层关闭两遍。
const metaLeadClosed = "lead_closed"

// metaLead 是线索写法的存储键（与 Node.Lead 同一事实）。
const metaLead = "lead"

// LeadClosure 是一条线索因何关闭：类别与理由。
type LeadClosure struct {
	Kind   LeadClosureKind `json:"kind"`
	Reason string          `json:"reason"`
}

// Category 返回观察的类别（非观察或内容缺失时为空）。
func (n *Node) Category() ObservationCategory {
	if !n.IsObservation() || len(n.Content) == 0 {
		return ""
	}
	var c struct {
		Category ObservationCategory `json:"category"`
	}
	if json.Unmarshal(n.Content, &c) != nil {
		return ""
	}
	return c.Category
}

// Statement 返回事实观察的陈述（非事实观察或内容缺失时为空）。线索是事实观察，它的
// 陈述就是这条线索说了什么。
func (n *Node) Statement() string {
	if !n.IsFact() || len(n.Content) == 0 {
		return ""
	}
	var c struct {
		Statement string `json:"statement"`
	}
	if json.Unmarshal(n.Content, &c) != nil {
		return ""
	}
	return c.Statement
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
	return n.refuteMark().RefuteReason
}

// RefuteDetail 返回评估者证伪时给出的理由（节点 metadata 的 refute_detail；机器判出的证伪
// 没有理由，返回空）。原因是类别，理由说明缺了什么证据、哪部分已被证明。
func (n *Node) RefuteDetail() string {
	return n.refuteMark().RefuteDetail
}

type refuteMark struct {
	RefuteReason string `json:"refute_reason"`
	RefuteDetail string `json:"refute_detail"`
}

func (n *Node) refuteMark() refuteMark {
	var m refuteMark
	if len(n.Metadata) == 0 || json.Unmarshal(n.Metadata, &m) != nil {
		return refuteMark{}
	}
	return m
}

// NormalizePriority 把模型或接口给出的优先级词归一到 Priority（未知值归 medium）。
// 执行者写假设与用户指派动作共用本判定。
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
