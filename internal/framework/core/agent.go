package core

import "context"

// Agent 是框架中所有智能体的统一接口：
// Planner、Executor、Evaluator、Monitor 均实现该接口。
type Agent interface {
	// Name 返回 Agent 的唯一名称
	Name() string

	// Run 启动 Agent 的主循环（阻塞直到完成或取消）
	Run(ctx context.Context) error

	// Stop 停止 Agent（优雅关闭）
	Stop(ctx context.Context) error

	// Recoverable 支持状态恢复
	Recoverable
}
