// Package actor 定义 Agent 系统的核心领域类型。
//
// 依赖方向：actor → registry → provider
// 所有跨包共享的领域模型在此定义，避免循环依赖。
package actor

import (
	"encoding/json"
	"time"

	"github.com/V3teran/liusha/internal/registry"
)

// ─────────────────────────────────────────────
//  Move（策略意图单元）
// ─────────────────────────────────────────────

type MoveKind string

const (
	MoveKindEnumerate MoveKind = "enumerate" // 信息收集/枚举（web目录、端口、云资产、域对象、CTF初探）
	MoveKindProbe     MoveKind = "probe"     // 漏洞探测/弱点发现（扫描/fuzz/初步PoC，不坐实）
	MoveKindExploit   MoveKind = "exploit"   // 漏洞利用/坐实（PoC确认、初始立足点获取）
	MoveKindEscalate  MoveKind = "escalate"  // 权限提升/横向移动（本机提权/域提权/云IAM/跨主机）
	MoveKindPersist   MoveKind = "persist"   // 后渗透（数据采集/持久化/C2/影响评估）
)

// LandmarkRef 唯一标识一个目标节点，域无关三元组。
type LandmarkRef struct {
	Domain  string `json:"domain"`   // "web" | "binary" | "cloud" | "lateral"
	RefKind string `json:"ref_kind"` // endpoint | file | resource | node | ...
	Locator string `json:"locator"`  // 域内寻址，url-encoded
}

// Key 返回规范化唯一键：domain:ref_kind:locator
func (r LandmarkRef) Key() string {
	return r.Domain + ":" + r.RefKind + ":" + r.Locator
}

// Display 返回人类可读短文本。
func (r LandmarkRef) Display() string {
	if r.Locator != "" {
		return r.Domain + "/" + r.RefKind + ":" + r.Locator
	}
	return r.Domain + "/" + r.RefKind
}

// Move 是 Planner 生成的单个战术意图单元。
type Move struct {
	ID          string
	Kind        MoveKind
	Target      LandmarkRef
	Objective   string
	Cues        []string
	Constraints []registry.Constraint
	Priority    int
	DependsOn   []string // 依赖的 Move.ID，空=无依赖（可并发）
}

// MoveStatus 是 Move 的执行状态。
type MoveStatus string

const (
	MoveStatusInFlight  MoveStatus = "in_flight"
	MoveStatusDone      MoveStatus = "done"
	MoveStatusAbandoned MoveStatus = "abandoned"
	MoveStatusStalled   MoveStatus = "stalled"
)

// MoveRecord 携带 Move 执行结果，供 Planner 下轮决策。
type MoveRecord struct {
	Move
	Status   MoveStatus
	HaltWhy  string   // Critic/Budget 给的终止原因摘要
	Findings []string // Finding.ID 列表
}

// ─────────────────────────────────────────────
//  Landmark / Finding（世界模型 + 报告实体）
// ─────────────────────────────────────────────

type LandmarkKind string

const (
	LandmarkTarget     LandmarkKind = "target"
	LandmarkService    LandmarkKind = "service"
	LandmarkEndpoint   LandmarkKind = "endpoint"
	LandmarkCredential LandmarkKind = "credential"
	LandmarkWeakness   LandmarkKind = "weakness"
	LandmarkSession    LandmarkKind = "session"
	LandmarkArtifact   LandmarkKind = "artifact"
	LandmarkGoal       LandmarkKind = "goal"
)

type LandmarkState string

const (
	LandmarkHypothesized LandmarkState = "hypothesized"
	LandmarkConfirmed    LandmarkState = "confirmed"
	LandmarkRefuted      LandmarkState = "refuted"
)

type Landmark struct {
	ID         string
	Ref        LandmarkRef
	Kind       LandmarkKind
	State      LandmarkState
	Summary    string  // ≤1 行，注入 Planner/Actor 系统提示
	Detail     string  // 完整内容，read_landmark(id) 按需拉取
	Signals    []registry.Signal
	Confidence float64 // 0~1，基于 Signal 权重加权均值
	TaskID     string
	MoveID     string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// CalcConfidence 按 Signal 权重均值（前三条）计算 Confidence。
func CalcConfidence(signals []registry.Signal) float64 {
	if len(signals) == 0 {
		return 0
	}
	top := signals
	if len(top) > 3 {
		top = top[:3]
	}
	var sum float64
	for _, s := range top {
		sum += registry.SignalWeight(s.Kind)
	}
	v := sum / float64(len(top))
	if v > 1.0 {
		v = 1.0
	}
	return v
}

type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityHigh     Severity = "high"
	SeverityMedium   Severity = "medium"
	SeverityLow      Severity = "low"
	SeverityInfo     Severity = "info"
)

type FindingStatus string

const (
	FindingStatusOpen          FindingStatus = "open"
	FindingStatusConfirmed     FindingStatus = "confirmed"
	FindingStatusFixed         FindingStatus = "fixed"
	FindingStatusFalsePositive FindingStatus = "false_positive"
	FindingStatusAccepted      FindingStatus = "accepted"
)

type Finding struct {
	ID            string
	TaskID        string
	LandmarkID    string
	Title         string
	Severity      Severity
	Description   string
	Evidence      []registry.Signal
	Remediation   string
	DependsOn     []string
	MoveID        string
	CWEID         string        // "CWE-89"，去重键
	OWASPCategory string        // "A03:2021"
	Repro         json.RawMessage
	Status        FindingStatus
	TriageNote    string
	TriagedAt     *time.Time
	Seq           int64
	CreatedAt     time.Time
}

// ─────────────────────────────────────────────
//  Actor 核心类型
// ─────────────────────────────────────────────

type HaltReason string

const (
	HaltDone       HaltReason = "done"
	HaltBudget     HaltReason = "budget"
	HaltWatchdog   HaltReason = "watchdog"
	HaltConstraint HaltReason = "constraint"
	HaltCancelled  HaltReason = "cancelled"
	HaltError      HaltReason = "error"
)

// Budget 是单次 Move 的执行预算。
type Budget struct {
	MaxSteps          int
	MaxTokens         int
	WatchdogSecs      int
	CompactionTrigger float64 // 默认 0.70，触发上下文压缩
	CriticInterval    int     // 默认 5，每 N 步评估一次
}

func DefaultBudget() Budget {
	return Budget{
		MaxSteps:          50,
		MaxTokens:         100000,
		WatchdogSecs:      300,
		CompactionTrigger: 0.70,
		CriticInterval:    5,
	}
}

// SettleConfig 是结算阶段配置。
type SettleConfig struct {
	Threshold    float64  // 默认 0.85，触发结算指令注入
	Directive    string   // 结算指令（per MoveKind 不同，由 Profile 定义）
	AllowedTools []string // 结算阶段只开放这些工具
}

// ScanBudget 是 Scan 级别总预算。
type ScanBudget struct {
	MaxMoves           int
	MaxTotalTime       time.Duration
	MaxConcurrentMoves int // 并发 Move 上限，默认 3
}

func DefaultScanBudget() ScanBudget {
	return ScanBudget{
		MaxMoves:           100,
		MaxTotalTime:       4 * time.Hour,
		MaxConcurrentMoves: 3,
	}
}

type ScanBudgetRemaining struct {
	RemainingMoves int
	RemainingTime  time.Duration
}

// ─────────────────────────────────────────────
//  Step / Execution（执行记录）
// ─────────────────────────────────────────────

// Step 是 Actor 的一次 ReAct 步。
type Step struct {
	Index      int
	Thought    string // LLM 文本部分（tool call 之前的推理）
	Hypotheses []string
}

// Execution 是 Dispatcher 对单个 Move 的一次完整执行包装。
// 一个 Move 可被执行多次（Critic Steer 后重跑）。
type Execution struct {
	Index      int
	Result     ActorResult
	Steer      string   // Critic 给的 steering 文本，首次为空
	Hypotheses []string // 本次 Execution 结束时的 Working Memory
}

// ActorReq 是 Actor.Run 的输入。
type ActorReq struct {
	System             string             // 不参与压缩：Profile.SystemPrompt + Landmark summaries
	Inbox              []Message          // 参与压缩：初始指令
	Hypotheses         []string           // Working Memory
	Budget             Budget
	Settle             SettleConfig
	PendingConstraints []registry.Constraint
}

// Message 是 Actor 内部会话消息（薄包装，转发给 Provider）。
type Message struct {
	Role    string
	Content string
}

// ActorResult 是 Actor.Run 的输出。
type ActorResult struct {
	Steps      []Step
	Conclusion string
	Halt       HaltReason
	TokensUsed int
}

// ─────────────────────────────────────────────
//  Critic
// ─────────────────────────────────────────────

type Verdict string

const (
	VerdictContinue Verdict = "continue"
	VerdictSteer    Verdict = "steer"
	VerdictAbandon  Verdict = "abandon"
)

type Assessment struct {
	Advancing   bool
	Observation string
	Verdict     Verdict
}

// Critic 评估 Actor 执行进展，决定是否继续/引导/放弃。
type Critic interface {
	Evaluate(ctx interface{ Deadline() (time.Time, bool) }, move Move, recent []Step) (Assessment, error)
}

// ─────────────────────────────────────────────
//  Planner 相关
// ─────────────────────────────────────────────

// PlannerState 是 Planner 跨轮的策略记忆，schema 固定防止漂移。
type PlannerState struct {
	HumanCues     []string          `json:"human_cues"`
	StrategyNotes string            `json:"strategy_notes"`
	Hypotheses    []string          `json:"hypotheses"`
	Extensions    map[string]string `json:"extensions"` // 仅允许：domain_notes, priority_override
}

// PlanReq 是 Planner.Plan 的输入（分级，无全量 Landmarks）。
type PlanReq struct {
	TaskID           string
	Frontier         []Landmark        // confirmed 且无出边，通常 <20
	TopKLandmarks    []Landmark        // 按相关性 Top-30 Summary
	History          []MoveRecord
	State            PlannerState
	EnabledMoveKinds []MoveKind
	Budget           ScanBudgetRemaining
}

// PlanHalt 是 Planner 给出的终止信号。
type PlanHalt struct {
	Reason string
}

// PlanResult 是 Planner.Plan 的输出。
type PlanResult struct {
	Moves []Move
	State PlannerState
	Halt  *PlanHalt // nil = 继续
}

// ─────────────────────────────────────────────
//  Campaign / Assignment
// ─────────────────────────────────────────────

type Target struct {
	ID    string
	Host  string
	Brief string
}

// Campaign 是前端可配置的扫描策略。
type Campaign struct {
	EnabledMoveKinds   []MoveKind
	BudgetOverrides    map[MoveKind]Budget
	GlobalConstraints  []registry.Constraint
	ScanBudget         ScanBudget
	PostScanHook       func(taskID string) // 扫描结束后自动触发，非 MoveKind
}

type Assignment struct {
	ID      string
	Targets []Target
	Campaign Campaign
}

// ─────────────────────────────────────────────
//  Directive（人工干预）
// ─────────────────────────────────────────────

type DirectiveKind string

const (
	DirectiveGuidance   DirectiveKind = "guidance"
	DirectiveConstraint DirectiveKind = "constraint"
	DirectiveHalt       DirectiveKind = "halt"
)

type Directive struct {
	Kind       DirectiveKind
	Content    string
	Constraint *registry.Constraint
}
