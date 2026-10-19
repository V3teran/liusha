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

	// RelationConfirms 表示确认关系（evidence → observation）
	// 语义：executor 侧证据（content.type=evidence 的 observation）确认假设
	RelationConfirms RelationKind = "confirms"

	// RelationRefutes 表示反驳关系（evidence → observation）
	// 语义：executor 侧证据反驳假设
	RelationRefutes RelationKind = "refutes"

	// RelationEnables 表示使能关系（result → action）
	// 语义：结果使能新动作。预留——当前无生产者（planner 派生用 triggers）
	RelationEnables RelationKind = "enables"

	// RelationDependsOn 表示依赖关系（action → action）
	// 语义：动作依赖另一个动作完成
	RelationDependsOn RelationKind = "depends_on"

	// RelationBelongsTo 表示归属关系（action → objective）
	// 语义：动作服务于目标。预留——当前无生产者
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

// ObservationConfidence 是观察的图级状态。探索图是领域中立的认知架构：
// 这里只有「主张」与「事实」，领域语义（如安全场景的漏洞）由章程与
// 领域存储承载，不进图本体。
//
// 两条互不重叠的生命周期：
//   - 假设（category=hypothesis）：unverified → verified/refuted，流转由
//     evaluator 复现门独占；verified 的唯一产物是 result 节点——result
//     只承载经裁决坐实的主张，事实永远不晋升为 result。
//   - 事实（category=fact）：factual 写入即终态，无流转——事实是世界
//     状态断言，信任来自执行轨迹出处而非复现；planner 直接消费。
type ObservationConfidence string

const (
	// ConfidenceUnverified 待裁决（假设生命周期由 evaluator 复现门独占流转）。
	ConfidenceUnverified ObservationConfidence = "unverified"
	// ConfidenceVerified 已坐实（晋升 result）。
	ConfidenceVerified ObservationConfidence = "verified"
	// ConfidenceRefuted 已证伪。
	ConfidenceRefuted ObservationConfidence = "refuted"
	// ConfidenceFactual 已确认事实（executor 写入即终态）。
	ConfidenceFactual ObservationConfidence = "factual"
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

// IsVerified 判断观察是否已验证（通过复现门或已确认事实）
func (c ObservationConfidence) IsVerified() bool {
	return c == ConfidenceVerified || c == ConfidenceFactual
}

// IsRefuted 判断观察是否已证伪
func (c ObservationConfidence) IsRefuted() bool {
	return c == ConfidenceRefuted
}

// IsVulnerabilityToBeTested 判断是否是待复现门裁决的漏洞假设。
func (c ObservationConfidence) IsVulnerabilityToBeTested() bool {
	return c == ConfidenceUnverified
}

// IsFact 判断是否是事实终态（executor 写入即终态，无流转）。
func (c ObservationConfidence) IsFact() bool {
	return c == ConfidenceFactual
}

// String 返回节点类型的字符串表示
func (k NodeKind) String() string {
	return string(k)
}

// String 返回动作状态的字符串表示
func (s ActionState) String() string {
	return string(s)
}
