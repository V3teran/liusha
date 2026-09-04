// Package executor 定义 Agent 系统的核心领域类型。
//
// 依赖方向：actor → registry → provider
// 所有跨包共享的领域模型在此定义，避免循环依赖。
package executor

import (
	"time"

	"github.com/V3teran/liusha/internal/registry"
	"github.com/V3teran/liusha/internal/worldmodel"
)

// ─────────────────────────────────────────────
//  Action（执行意图单元）
// ─────────────────────────────────────────────
// Complexity 已迁移至 worldmodel 包

// TargetRef 唯一标识一个目标节点，域无关三元组。
type TargetRef struct {
	Domain  string `json:"domain"`   // "web" | "binary" | "cloud" | "lateral"
	RefKind string `json:"ref_kind"` // endpoint | file | resource | node | ...
	Locator string `json:"locator"`  // 域内寻址，url-encoded
}

// Key 返回规范化唯一键：domain:ref_kind:locator
func (r TargetRef) Key() string {
	return r.Domain + ":" + r.RefKind + ":" + r.Locator
}

// Display 返回人类可读短文本。
func (r TargetRef) Display() string {
	if r.Locator != "" {
		return r.Domain + "/" + r.RefKind + ":" + r.Locator
	}
	return r.Domain + "/" + r.RefKind
}

// Action 是 Planner 生成的单个执行意图单元。
type Action struct {
	ID          string
	Complexity  worldmodel.Complexity
	Target      TargetRef
	Instruction string // 自然语言描述要做什么
	Cues        []string
	Constraints []registry.Constraint
	Priority    int
	DependsOn   []string // 依赖的其他 Action.ID，空=无依赖（可并发）
}

// ActionStatus 是 Action 的执行状态。
type ActionStatus string

const (
	ActionStatusInFlight  ActionStatus = "in_flight"
	ActionStatusDone      ActionStatus = "done"
	ActionStatusAbandoned ActionStatus = "abandoned"
	ActionStatusStalled   ActionStatus = "stalled"
)

// ActionRecord 携带 Action 执行结果，供 Planner 下轮决策。
type ActionRecord struct {
	Action
	Status   ActionStatus
	HaltWhy  string   // Budget/Done 给的终止原因摘要
	Findings []string // Finding.ID 列表
}

// ─────────────────────────────────────────────
//  Executor 核心类型
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

// Budget 是单次 Action 的执行预算。
type Budget struct {
	MaxSteps          int
	MaxTokens         int
	WatchdogSecs      int
	CompactionTrigger float64 // 默认 0.70，触发上下文压缩
}

func DefaultBudget() Budget {
	return Budget{
		MaxSteps:          50,
		MaxTokens:         100000,
		WatchdogSecs:      300,
		CompactionTrigger: 0.70,
	}
}

// SettleConfig 是结算阶段配置。
type SettleConfig struct {
	Threshold    float64  // 默认 0.85，触发结算指令注入
	Directive    string   // 结算指令（per Complexity 不同，由 Profile 定义）
	AllowedTools []string // 结算阶段只开放这些工具
}

// ScanBudget 是 Scan 级别总预算。
type ScanBudget struct {
	MaxActions           int
	MaxTotalTime         time.Duration
	MaxConcurrentActions int // 并发 Action 上限，默认 3
}

func DefaultScanBudget() ScanBudget {
	return ScanBudget{
		MaxActions:           100,
		MaxTotalTime:         4 * time.Hour,
		MaxConcurrentActions: 3,
	}
}

type ScanBudgetRemaining struct {
	RemainingActions int
	RemainingTime    time.Duration
}

// ─────────────────────────────────────────────
//  Step / Execution（执行记录）
// ─────────────────────────────────────────────

// Step 是 Executor 的一次 ReAct 步。
type Step struct {
	Index       int
	Thought     string // LLM 文本部分（tool call 之前的推理）
	ToolCalls   []ToolCall
	ToolResults []ToolResult
	Hypotheses  []string
}

// ToolCall 是工具调用记录。
type ToolCall struct {
	ID   string
	Name string
	Args string
}

// ToolResult 是工具执行结果。
type ToolResult struct {
	ToolCallID string
	Output     string
	Error      string
}

// Execution 是 Dispatcher 对单个 Action 的一次完整执行包装。
type Execution struct {
	Index      int
	Result     ExecutorResult
	Hypotheses []string // 本次 Execution 结束时的 Working Memory
}

// ExecutorReq 是 Actor.Run 的输入。
type ExecutorReq struct {
	System             string    // 不参与压缩：Profile.SystemPrompt + Landmark summaries
	Inbox              []Message // 参与压缩：初始指令
	Hypotheses         []string  // Working Memory
	Budget             Budget
	Settle             SettleConfig
	PendingConstraints []registry.Constraint
}

// Message 是 Executor 内部会话消息（薄包装，转发给 Provider）。
type Message struct {
	Role    string
	Content string
}

// ExecutorResult 是 Actor.Run 的输出。
type ExecutorResult struct {
	Steps      []Step
	Conclusion string
	Halt       HaltReason
	TokensUsed int
}

// ─────────────────────────────────────────────
//  Self-Monitoring
// ─────────────────────────────────────────────

// SelfAssessment 是自我评估结果。
type SelfAssessment struct {
	Status     string // "on_track" | "off_track" | "stalled"
	Severity   string // "low" | "medium" | "high"
	Reasoning  string
	Correction string // 如果跑偏，如何纠正
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
	EnabledComplexity []worldmodel.Complexity
	BudgetOverrides   map[worldmodel.Complexity]Budget
	GlobalConstraints []registry.Constraint
	ScanBudget        ScanBudget
	PostScanHook      func(taskID string)
}

type Assignment struct {
	ID       string
	Targets  []Target
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
