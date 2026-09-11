package runtime

import (
	"context"

	"github.com/V3teran/liusha/internal/framework/core"
)

// Orchestrator 是多 Agent 协调器。
// 负责任务的生命周期管理、Agent 调度、状态同步。
type Orchestrator interface {
	// Run 运行任务（阻塞直到完成）
	Run(ctx context.Context) error

	// Resume 从 checkpoint 恢复任务
	Resume(ctx context.Context, checkpointID core.CheckpointID) error

	// Pause 暂停任务（保存 checkpoint）
	Pause(ctx context.Context) (core.CheckpointID, error)

	// Cancel 取消任务
	Cancel(ctx context.Context) error

	// Status 获取任务状态
	Status() OrchestratorStatus
}

// OrchestratorStatus 是任务执行状态。
type OrchestratorStatus struct {
	// 任务 ID
	TaskID string `json:"task_id"`

	// 执行状态：idle, running, paused, completed, failed, canceled
	State string `json:"state"`

	// 当前阶段
	Phase string `json:"phase"`

	// 已运行时间（毫秒）
	ElapsedMs int64 `json:"elapsed_ms"`

	// 进度百分比（0-100）
	Progress int `json:"progress"`

	// 各 Agent 的状态
	AgentStates map[string]string `json:"agent_states"`

	// 最后更新时间（Unix 毫秒）
	UpdatedAt int64 `json:"updated_at"`
}

// OrchestratorConfig 是 Orchestrator 配置。
type OrchestratorConfig struct {
	// 任务 ID
	TaskID string

	// 注册的 Agent 列表
	Agents []core.Agent

	// 状态管理器
	StateManager core.StateManager[any]

	// 检查点管理器
	Checkpointer core.Checkpointer

	// 事件总线
	EventBus core.EventBus

	// 检查点策略
	CheckpointStrategy core.CheckpointStrategy

	// Agent 钩子
	AgentHooks core.AgentHooks

	// 最大并发 Agent 数
	MaxConcurrentAgents int

	// 任务超时时间（毫秒，0 表示无限制）
	TimeoutMs int64
}

// OrchestratorFactory 创建 Orchestrator。
type OrchestratorFactory func(config OrchestratorConfig) (Orchestrator, error)

// SubtaskRunner 是子任务运行器。
type SubtaskRunner interface {
	// Run 运行子任务（阻塞直到完成）
	Run(ctx context.Context, config SubtaskConfig) (*SubtaskResult, error)

	// RunAsync 异步运行子任务（返回任务 ID）
	RunAsync(ctx context.Context, config SubtaskConfig) (string, error)

	// Wait 等待异步子任务完成
	Wait(ctx context.Context, taskID string) (*SubtaskResult, error)
}

// SubtaskConfig 是子任务配置。
type SubtaskConfig struct {
	// 父任务 ID
	ParentTaskID string

	// 子任务目标
	Objective string

	// 超时时间（毫秒，0 表示继承父任务）
	TimeoutMs int64

	// 是否隔离（独立知识图谱）
	Isolated bool

	// 继承的上下文数据
	Context map[string]any
}

// SubtaskResult 是子任务结果。
type SubtaskResult struct {
	// 子任务 ID
	TaskID string `json:"task_id"`

	// 是否成功
	Success bool `json:"success"`

	// 输出数据
	Output any `json:"output"`

	// 执行耗时（毫秒）
	DurationMs int64 `json:"duration_ms"`

	// 错误信息
	Error string `json:"error,omitempty"`
}
