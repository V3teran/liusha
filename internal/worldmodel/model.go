// Package worldmodel 实现攻击图世界模型（L3 状态层）的领域模型与持久化。
//
// 边界：只存已确证/假定的世界状态（target/asset/credential/access/finding），
// 过程细节（gdb 单步、每条 HTTP 报文）归 investigation-trace 投影 + traffic 存储，绝不进本层。
// 在途认知（lead/observation）留 Redis 工作记忆，经 Verifier 晋升门确证后才写入本层。
package worldmodel

import (
	"encoding/json"
	"time"
)

// NodeKind 是世界模型节点的 5 种持久类型。
// 判别列驱动单表 wm_node（非每类一张表），加新种类不改表结构。
type NodeKind string

const (
	KindTarget     NodeKind = "target"     // 交战目标（TargetRef 泛化 task.target_host）
	KindAsset      NodeKind = "asset"       // 发现的资产/攻击面（endpoint/file/bucket/port）
	KindCredential NodeKind = "credential"  // 凭据（多态 scheme，不再限 HTTP 三注入位）
	KindAccess     NodeKind = "access"      // 立足点（shell/session/iam 身份）——多阶段核心
	KindFinding    NodeKind = "finding"     // 坐实的漏洞（有 evidence/repro）
)

// EdgeRel 是世界模型边的 7 种关系。攻击链 = enables 边的路径。
type EdgeRel string

const (
	// 原有 3 种：节点间关系
	RelDerives EdgeRel = "derives" // 认知因果：A 推出 B（Finding → Credential）
	RelEnables EdgeRel = "enables" // 能力使能：Credential→Access、Access→Asset（攻击链路径）
	RelOn      EdgeRel = "on"      // 归属附着：Finding on Asset、Asset on Target

	// 新增 4 种：Provenance（执行溯源）
	RelSpawns   EdgeRel = "spawns"   // Node → Move：节点触发执行（Asset spawns probe Move）
	RelProduces EdgeRel = "produces" // Move → Node：Move 产出节点（exploit Move produces Access）
	RelPromotes EdgeRel = "promotes" // Node → Node：晋升关系（Lead promotes to Finding）
	RelRefutes  EdgeRel = "refutes"  // Node → Node：推翻假设（Verification refutes Finding）
)

// Confidence 是节点的确证程度。图里只允许这两态；Verifier 通过才置 confirmed。
type Confidence string

const (
	ConfConfirmed Confidence = "confirmed"
	ConfAssumed   Confidence = "assumed"
)

// TargetRef 是多态目标标识（契约一），取代裸 host 字符串。
// Locator 语义仅由对应 Domain Profile 解释，核心永不 parse。
type TargetRef struct {
	Domain  string `json:"domain"`   // web|binary|cloud|lateral，由 Profile 定义
	RefKind string `json:"ref_kind"` // endpoint|file|resource|node|...
	Locator string `json:"locator"`  // 域内寻址
}

// Node 是 wm_node 表行的 Go 表示。
//
// Attrs 载荷形状由 Kind 决定（Profile 定 evidence 细节）：
//   - target:     scope, roe_tag
//   - asset:      service, port, tech, discovered_via
//   - credential: scheme, material(密文，cryptx 加密), grants[]
//   - access:     access_type(shell/session/iam), privilege
//   - finding:    severity, taxonomy[], evidence, repro[]
type Node struct {
	ID         string
	TaskID     string
	Kind       NodeKind
	Ref        TargetRef // 展开为 domain/ref_kind/locator 三列
	Attrs      json.RawMessage
	Confidence Confidence
	VerifiedBy *string // 指向 wm_verification.id；assumed 节点为 nil
	Seq        int64   // 对外稳定短号
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Edge 是 wm_edge 表行的 Go 表示。
type Edge struct {
	ID        string
	TaskID    string
	Rel       EdgeRel
	Src       string // wm_node.id
	Dst       string // wm_node.id
	Attrs     json.RawMessage
	CreatedAt time.Time
}

// VerifyOutcome 是 Verifier 晋升门的复检结论。
type VerifyOutcome string

const (
	OutcomeConfirmed VerifyOutcome = "confirmed"
	OutcomeRefuted   VerifyOutcome = "refuted"
)

// Verification 是 wm_verification 表行：Lead → 图节点 每次复检的取证记录。
// 是可复现交付 + 合规审计的证据链源。
type Verification struct {
	ID         string
	TaskID     string
	LeadID     string
	Primitives json.RawMessage // 回放了哪些 L1 原语
	Outcome    VerifyOutcome
	Evidence   json.RawMessage // 复现证据
	DurationMs int64
	CreatedAt  time.Time
}

// MoveStatus 是 Move 节点的执行状态。
type MoveStatus string

const (
	MoveStatusPending   MoveStatus = "pending"   // 待执行（Planner 派发但未开始）
	MoveStatusActive    MoveStatus = "active"    // 执行中（Actor 正在处理）
	MoveStatusDone      MoveStatus = "done"      // 已完成（正常结束）
	MoveStatusAbandoned MoveStatus = "abandoned" // 已放弃（中途取消/失败）
)

// Move 是 wm_move 表行：追踪规划器派发的每次 Move 执行。
// Move 作为节点入图后，可追溯"哪个 Move 产出了哪些节点"（produces 边）。
type Move struct {
	ID         string
	TaskID     string
	Kind       string // enumerate/probe/exploit/escalate/persist
	Status     MoveStatus
	TargetNode string          // 在哪个 wm_node.id 上执行此 Move
	Reason     string          // Planner 给出的执行原因（展示给用户）
	Outcome    json.RawMessage // Move 执行结果摘要（final_text/tool_calls/cognition）
	CreatedAt  time.Time
	UpdatedAt  time.Time
}
