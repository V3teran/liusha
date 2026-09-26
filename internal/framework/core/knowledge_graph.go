package core

// ExplorationGraph 标准定义
//
// 设计原则：
// 1. 对齐 ReAct 模式（Thought → Action → Observation）
// 2. 对齐 PDDL 标准（Objective → Action → State）
// 3. 通用认知循环：Objective → Action → Observation → Result
//
// 这些类型不是业务特定的，而是所有 AI Agent 的通用认知模式

// NodeKind 是探索图节点的标准类型
type NodeKind string

const (
	// KindObjective 表示任务目标节点
	// 对应：用户输入、任务描述、期望结果
	// 来源：用户或上层系统
	KindObjective NodeKind = "objective"

	// KindAction 表示执行动作节点
	// 对应：计划的执行步骤、工具调用、操作指令
	// 来源：Planner Agent
	KindAction NodeKind = "action"

	// KindObservation 表示观察结果节点
	// 对应：执行输出、环境反馈、传感器数据
	// 来源：Executor Agent
	KindObservation NodeKind = "observation"

	// KindEvaluation 表示评估结论节点（已废弃，保留用于兼容性）
	// 对应：结果验证、质量评分、可信度判断
	// 来源：Evaluator Agent
	KindEvaluation NodeKind = "evaluation"

	// KindResult 表示最终结果节点
	// 对应：已确认的发现、完成的任务、产出物
	// 来源：Evaluation 提升或直接确认
	KindResult NodeKind = "result"
)

// RelationKind 是探索图关系的标准类型
type RelationKind string

const (
	// RelationGenerates 表示生成关系（action → observation）
	// 语义：动作生成观察结果
	RelationGenerates RelationKind = "generates"

	// RelationConfirms 表示确认关系（evaluation → result）
	// 语义：评估确认结果为真
	RelationConfirms RelationKind = "confirms"

	// RelationRefutes 表示反驳关系（evaluation → observation）
	// 语义：评估否定观察结果
	RelationRefutes RelationKind = "refutes"

	// RelationEnables 表示使能关系（result → action）
	// 语义：结果使得新动作可执行
	RelationEnables RelationKind = "enables"

	// RelationDependsOn 表示依赖关系（action → action）
	// 语义：动作依赖另一个动作完成
	RelationDependsOn RelationKind = "depends_on"

	// RelationBelongsTo 表示归属关系（action → objective）
	// 语义：动作服务于目标
	RelationBelongsTo RelationKind = "belongs_to"

	// RelationTriggers 表示触发关系（result → objective/action）
	// 语义：结果触发新的目标或下一轮动作
	RelationTriggers RelationKind = "triggers"
)

// ActionState 是动作的执行状态
type ActionState string

const (
	// ActionStateOpen 待执行
	ActionStateOpen ActionState = "open"

	// ActionStateBlocked 被阻塞（依赖未满足）
	ActionStateBlocked ActionState = "blocked"

	// ActionStateRunning 执行中
	ActionStateRunning ActionState = "running"

	// ActionStateDone 已完成
	ActionStateDone ActionState = "done"

	// ActionStateFailed 执行失败
	ActionStateFailed ActionState = "failed"

	// ActionStateExhausted 已耗尽（重试次数用完）
	ActionStateExhausted ActionState = "exhausted"

	// ActionStateAborted 被中止
	ActionStateAborted ActionState = "aborted"
)

// ObservationConfidence 是观察的置信度
type ObservationConfidence string

const (
	// ConfidenceUnverified 未验证
	ConfidenceUnverified ObservationConfidence = "unverified"

	// ConfidenceVerified 已验证
	ConfidenceVerified ObservationConfidence = "verified"

	// ConfidenceRefuted 已证伪
	ConfidenceRefuted ObservationConfidence = "refuted"
)

// Priority 是节点的优先级
type Priority string

const (
	// PriorityCritical 关键（P0）
	PriorityCritical Priority = "critical"

	// PriorityHigh 高（P1）
	PriorityHigh Priority = "high"

	// PriorityMedium 中（P2）
	PriorityMedium Priority = "medium"

	// PriorityLow 低（P3）
	PriorityLow Priority = "low"
)

// EvaluationOutcome 是评估的结论
type EvaluationOutcome string

const (
	// OutcomeConfirmed 确认为真
	OutcomeConfirmed EvaluationOutcome = "confirmed"

	// OutcomeRefuted 确认为假
	OutcomeRefuted EvaluationOutcome = "refuted"

	// OutcomeUncertain 不确定
	OutcomeUncertain EvaluationOutcome = "uncertain"

	// OutcomePartial 部分正确
	OutcomePartial EvaluationOutcome = "partial"
)

// ActionComplexity 是动作的复杂度
type ActionComplexity int

const (
	// ComplexityTrivial 简单（<1 分钟）
	ComplexityTrivial ActionComplexity = 1

	// ComplexitySimple 简单（1-5 分钟）
	ComplexitySimple ActionComplexity = 2

	// ComplexityModerate 中等（5-30 分钟）
	ComplexityModerate ActionComplexity = 3

	// ComplexityComplex 复杂（30 分钟-2 小时）
	ComplexityComplex ActionComplexity = 4

	// ComplexityVeryComplex 非常复杂（>2 小时）
	ComplexityVeryComplex ActionComplexity = 5
)

// IsTerminal 判断动作状态是否为终态
func (s ActionState) IsTerminal() bool {
	return s == ActionStateDone ||
		s == ActionStateFailed ||
		s == ActionStateExhausted ||
		s == ActionStateAborted
}

// IsExecutable 判断动作状态是否可执行
func (s ActionState) IsExecutable() bool {
	return s == ActionStateOpen
}

// IsActive 判断动作状态是否活跃
func (s ActionState) IsActive() bool {
	return s == ActionStateRunning
}

// IsVerified 判断观察是否已验证
func (c ObservationConfidence) IsVerified() bool {
	return c == ConfidenceVerified
}

// IsRefuted 判断观察是否已证伪
func (c ObservationConfidence) IsRefuted() bool {
	return c == ConfidenceRefuted
}

// IsPositive 判断评估结论是否为正面
func (o EvaluationOutcome) IsPositive() bool {
	return o == OutcomeConfirmed || o == OutcomePartial
}

// IsNegative 判断评估结论是否为负面
func (o EvaluationOutcome) IsNegative() bool {
	return o == OutcomeRefuted
}

// String 返回节点类型的字符串表示
func (k NodeKind) String() string {
	return string(k)
}

// String 返回关系类型的字符串表示
func (r RelationKind) String() string {
	return string(r)
}

// String 返回动作状态的字符串表示
func (s ActionState) String() string {
	return string(s)
}

// String 返回评估结论的字符串表示
func (o EvaluationOutcome) String() string {
	return string(o)
}

// ValidNodeKinds 返回所有有效的节点类型
func ValidNodeKinds() []NodeKind {
	return []NodeKind{
		KindObjective,
		KindAction,
		KindObservation,
		KindEvaluation,
		KindResult,
	}
}

// ValidRelationKinds 返回所有有效的关系类型
func ValidRelationKinds() []RelationKind {
	return []RelationKind{
		RelationGenerates,
		RelationConfirms,
		RelationRefutes,
		RelationEnables,
		RelationDependsOn,
		RelationBelongsTo,
		RelationTriggers,
	}
}

// ValidActionStates 返回所有有效的动作状态
func ValidActionStates() []ActionState {
	return []ActionState{
		ActionStateOpen,
		ActionStateBlocked,
		ActionStateRunning,
		ActionStateDone,
		ActionStateFailed,
		ActionStateExhausted,
		ActionStateAborted,
	}
}

// ValidEvaluationOutcomes 返回所有有效的评估结论
func ValidEvaluationOutcomes() []EvaluationOutcome {
	return []EvaluationOutcome{
		OutcomeConfirmed,
		OutcomeRefuted,
		OutcomeUncertain,
		OutcomePartial,
	}
}
