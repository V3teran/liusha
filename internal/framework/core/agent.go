// Package core 的 Agent 生命周期契约。
//
// 四个业务 agent（planner/executor/evaluator/monitor）由 cmd/runner 以具体类型
// 装配并以 ctx 取消实现停机（见 agentLifecycle），无多态分发需求——
// 因此这里不再定义 Agent 接口（曾有，因零多态使用被移除）。
package core
