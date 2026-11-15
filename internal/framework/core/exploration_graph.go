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

	// NodeStateAborted 已中止：没有做完就被收回，不含对错判断——任务结束时仍未完成，
	// 或任务目标调整时撤回的未执行规划
	NodeStateAborted NodeState = "aborted"

	// NodeStateRejected 已否决（action 专用）：监察者或用户判定这一步不该做，附原因；
	// 规划者不得再提出等效动作
	NodeStateRejected NodeState = "rejected"

	// NodeStateSuperseded 已吸收（派生 objective 专用：范围由吸收它的新目标
	// 接管，本目标不再作为独立方向推进；与 supersedes 边同时落定，永不迁出）
	NodeStateSuperseded NodeState = "superseded"
)

// VerificationStatus 是假设观察的验证状态。探索图是领域中立的认知架构：
// 这里只有「假设」与「事实」，领域语义（如安全场景的漏洞）由章程与领域存储
// 承载，不进图本体。
//
// 只有假设（category=hypothesis）有验证状态：pending → confirmed/refuted，流转由
// evaluator 的复现门独占；confirmed 的唯一产物是 result 节点。事实没有验证状态
// （写入即终态，可信来自产出它的动作而非复现）；result 也没有（它就是坐实本身）。
// 审计链（exploration_verification）的每条记录也以它记结论，只取 confirmed / refuted。
type VerificationStatus string

const (
	// VerificationPending 待验证（假设写入时的状态）。
	VerificationPending VerificationStatus = "pending"
	// VerificationConfirmed 已坐实（晋升 result）。
	VerificationConfirmed VerificationStatus = "confirmed"
	// VerificationRefuted 已证伪（原因见节点的 refute_reason）。
	VerificationRefuted VerificationStatus = "refuted"
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
		s == NodeStateRejected ||
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
