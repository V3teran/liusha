package core

import "context"

// Agent 是框架中所有智能体的统一接口。
// Planner、Executor、Evaluator、Monitor 均实现该接口。
//
// 设计参考：
// - Kubernetes Operator (Reconciler)
// - Temporal Activities
// - Dapr Actors
type Agent interface {
	// Name 返回 Agent 的唯一名称（用于标识、日志、监控）
	Name() string

	// Run 启动 Agent 的主循环（阻塞直到完成或取消）
	// 应该是可重入的、幂等的
	Run(ctx context.Context) error

	// Stop 停止 Agent（优雅关闭）
	// 应该在合理时间内返回，释放所有资源
	Stop(ctx context.Context) error
}

// RecoverableAgent 是支持状态恢复的 Agent（可选能力）
//
// 实现此接口的 Agent 可以在故障后从检查点恢复状态。
// 符合 Go 接口设计最佳实践：小接口组合，而非大而全的接口。
type RecoverableAgent interface {
	Agent

	// SaveCheckpoint 保存当前状态到检查点
	SaveCheckpoint(ctx context.Context) (CheckpointID, error)

	// RestoreFromCheckpoint 从检查点恢复状态
	RestoreFromCheckpoint(ctx context.Context, id CheckpointID) error
}
