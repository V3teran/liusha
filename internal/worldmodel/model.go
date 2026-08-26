// Package worldmodel 实现统一的世界模型（认知图）。
//
// 设计决策（2026-08-26）：
// 1. 合并 Move 和 Node：所有认知单元在一张表
// 2. 4 种 NodeKind：objective（目标）/move（招法）/observation（观察）/discovery（发现）
// 3. 2 级 Confidence：unverified/verified
// 4. 独立边表：精确溯源和反向查询
//
// 边界：
// - Move（kind=move）：待执行的计划，有 state（open/running/done...）
// - Observation/Discovery：执行产出的知识，有 confidence（unverified/verified）
package worldmodel

import (
	"encoding/json"
	"time"
)

// NodeKind 是认知节点的类型
type NodeKind string

const (
	KindObjective   NodeKind = "objective"   // 任务目标（用户设定）
	KindMove        NodeKind = "move"        // 执行招法（Planner 生成）
	KindObservation NodeKind = "observation" // 观察记录（Executor 产出）
	KindDiscovery   NodeKind = "discovery"   // 重要发现（漏洞/凭据/立足点）
)

// State 是 Move 节点的执行状态
type State string

const (
	StateOpen      State = "open"      // 待执行
	StateBlocked   State = "blocked"   // 阻塞（等待依赖/资源）
	StateRunning   State = "running"   // 执行中
	StateDone      State = "done"      // 完成
	StateFailed    State = "failed"    // 失败
	StateExhausted State = "exhausted" // 耗尽（多次失败放弃）
	StateAborted   State = "aborted"   // 中止（手动停止）
)

// Confidence 是观察/发现的置信度
type Confidence string

const (
	ConfUnverified Confidence = "unverified" // 未验证（Executor 产出）
	ConfVerified   Confidence = "verified"   // 已验证（Verifier 复现）
)

// Complexity 是 Move 的执行复杂度（决定资源配额）
type Complexity string

const (
	ComplexityTrivial  Complexity = "trivial"  // 极简：<5 步
	ComplexitySimple   Complexity = "simple"   // 简单：~10 步
	ComplexityModerate Complexity = "moderate" // 中等：~30 步
	ComplexityComplex  Complexity = "complex"  // 复杂：~50 步
	ComplexityExtreme  Complexity = "extreme"  // 极限：~100 步
)

// Node 是世界模型的认知单元（统一表示 Move 和知识节点）
type Node struct {
	ID     string
	TaskID string
	Kind   NodeKind

	// 内容载荷（按 Kind 不同结构）
	Content json.RawMessage

	// Move 专用字段（kind=move 时使用）
	State         *State      // 执行状态
	Complexity    *Complexity // 执行复杂度
	DependsOn     []string    // 依赖的其他 Move ID
	BlockedReason *string     // 阻塞原因

	// Observation/Discovery 专用字段（kind=observation/discovery 时使用）
	Confidence *Confidence // 置信度

	// 通用字段
	Priority   int      // 优先级（所有节点）
	Owner      *string  // 执行者/创建者标识
	SourceType string   // 来源类型：user/planner/executor/verifier
	SourceID   string   // 来源 ID
	Tags       []string // 自由标签

	CreatedAt   time.Time
	UpdatedAt   time.Time
	CompletedAt *time.Time
}

// Relation 是节点间的关系类型
type Relation string

const (
	RelProduces Relation = "produces" // A 产出 B（move → observation/discovery）
	RelSupports Relation = "supports" // A 支持 B（observation → discovery，证据链）
	RelRefutes  Relation = "refutes"  // A 反驳 B
	RelEnables  Relation = "enables"  // A 使能 B（credential → access）
	RelDerives  Relation = "derives"  // A 推导出 B
	RelPartOf   Relation = "part_of"  // A 是 B 的一部分（port → host）
)

// Edge 是节点间的关系边
type Edge struct {
	TaskID    string
	SrcID     string
	Rel       Relation
	DstID     string
	Attrs     json.RawMessage
	CreatedAt time.Time
}

// TargetRef 是多态目标定位（保持不变）
type TargetRef struct {
	Domain  string // web/binary/cloud/network/lateral
	RefKind string // endpoint/file/host/port/service/function
	Locator string // 域内寻址（URL/路径/IP）
}

// VerifyOutcome 是 Verifier 的验证结论
type VerifyOutcome string

const (
	OutcomeConfirmed VerifyOutcome = "confirmed"
	OutcomeRefuted   VerifyOutcome = "refuted"
)

// Verification 是验证记录（审计链）
type Verification struct {
	ID         string
	TaskID     string
	NodeID     string // 被验证的节点 ID
	Primitives json.RawMessage
	Outcome    VerifyOutcome
	Evidence   json.RawMessage
	DurationMs int64
	CreatedAt  time.Time
}

// Source 是节点的来源信息
type Source struct {
	Type string // user/planner/executor/verifier
	ID   string // 具体标识
}

// IsMove 判断节点是否是 Move
func (n *Node) IsMove() bool {
	return n.Kind == KindMove
}

// IsKnowledge 判断节点是否是知识节点（observation/discovery）
func (n *Node) IsKnowledge() bool {
	return n.Kind == KindObservation || n.Kind == KindDiscovery
}

// CanExecute 判断 Move 是否可执行（无阻塞依赖）
func (n *Node) CanExecute(completed map[string]bool) bool {
	if !n.IsMove() {
		return false
	}
	if n.State == nil || *n.State != StateOpen {
		return false
	}
	for _, depID := range n.DependsOn {
		if !completed[depID] {
			return false
		}
	}
	return true
}
