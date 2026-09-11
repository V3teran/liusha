package core

import (
	"context"
	"encoding/json"
)

// Agent 是框架中所有智能体的统一接口。
// 可以是 Planner、Executor、Evaluator 或任何自定义角色。
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

// StatefulAgent 是有状态的 Agent（可读写任务状态）。
type StatefulAgent[T any] interface {
	Agent

	// GetState 获取当前任务状态
	GetState() *State[T]

	// UpdateState 更新任务状态
	UpdateState(ctx context.Context, updater func(*State[T]) error) error
}

// AgentConfig 是 Agent 的通用配置。
type AgentConfig struct {
	// Agent 名称
	Name string `json:"name"`

	// 任务 ID
	TaskID string `json:"task_id"`

	// 配置参数（业务自定义）
	Params json.RawMessage `json:"params"`

	// 超时时间（毫秒，0 表示无限制）
	TimeoutMs int64 `json:"timeout_ms,omitempty"`

	// 最大重试次数
	MaxRetries int `json:"max_retries,omitempty"`
}

// AgentFactory 是 Agent 的工厂函数。
// 业务层注册工厂，框架通过配置动态创建 Agent。
type AgentFactory func(config AgentConfig) (Agent, error)

// AgentRegistry 是 Agent 的注册中心。
type AgentRegistry interface {
	// Register 注册 Agent 工厂
	Register(agentType string, factory AgentFactory)

	// Create 根据类型和配置创建 Agent
	Create(agentType string, config AgentConfig) (Agent, error)

	// List 列出已注册的 Agent 类型
	List() []string

	// Exists 检查类型是否已注册
	Exists(agentType string) bool
}

// AgentMetrics 是 Agent 的运行指标。
type AgentMetrics struct {
	// Agent 名称
	Name string `json:"name"`

	// 运行状态：idle, running, stopped, failed
	Status string `json:"status"`

	// 已运行时间（毫秒）
	ElapsedMs int64 `json:"elapsed_ms"`

	// 处理的任务数
	TasksProcessed int64 `json:"tasks_processed"`

	// 失败次数
	FailureCount int64 `json:"failure_count"`

	// 自定义指标
	Custom map[string]interface{} `json:"custom,omitempty"`
}

// MonitorableAgent 是支持指标监控的 Agent。
type MonitorableAgent interface {
	Agent

	// Metrics 返回当前运行指标
	Metrics() AgentMetrics
}

// AgentHooks 是 Agent 生命周期钩子。
type AgentHooks interface {
	// BeforeRun 在 Agent 启动前调用
	BeforeRun(ctx context.Context, agent Agent) error

	// AfterRun 在 Agent 完成后调用（无论成功或失败）
	AfterRun(ctx context.Context, agent Agent, err error)

	// OnError 在 Agent 出错时调用
	OnError(ctx context.Context, agent Agent, err error) error
}
