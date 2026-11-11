// Package core 定义探索图的通用模型（节点 / 边的类型与状态）与图存储接口 GraphStore。
// 存储后端在 framework/persistence，业务语义在 internal/explorationgraph。
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

// 边关系枚举的权威定义在领域包 internal/explorationgraph（Relation，与 DB
// 约束 ck_exploration_edge_rel 严格一致）；本层 GraphNode.Rel 以 string 承载，
// 不在本层重复定义枚举（两处定义有漂移风险）。

// NodeState 是节点状态（GraphNode.State）：只有 action 与 objective 有状态，
// observation / result 为空（DB 约束 ck_state_by_kind）。
type NodeState string

const (
	// NodeStateOpen 待执行
	NodeStateOpen NodeState = "open"

	// NodeStateRunning 执行中
	NodeStateRunning NodeState = "running"

	// NodeStateCompleted 已完成
	NodeStateCompleted NodeState = "completed"

	// NodeStateFailed 执行失败
	NodeStateFailed NodeState = "failed"

	// NodeStateExhausted 已穷尽：action 是执行预算耗尽、经强制总结收尾；
	// 派生 objective 是任务结束时方向仍未达成
	NodeStateExhausted NodeState = "exhausted"

	// NodeStateAborted 被中止
	NodeStateAborted NodeState = "aborted"

	// NodeStateSuperseded 已吸收（派生 objective 专用：范围由吸收它的新目标
	// 接管，本目标不再作为独立方向推进；与 supersedes 边同时落定，永不迁出）
	NodeStateSuperseded NodeState = "superseded"
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
func (s NodeState) IsTerminal() bool {
	return s == NodeStateCompleted ||
		s == NodeStateFailed ||
		s == NodeStateExhausted ||
		s == NodeStateAborted ||
		s == NodeStateSuperseded
}

// String 返回节点类型的字符串表示
func (k NodeKind) String() string {
	return string(k)
}

// String 返回动作状态的字符串表示
func (s NodeState) String() string {
	return string(s)
}
