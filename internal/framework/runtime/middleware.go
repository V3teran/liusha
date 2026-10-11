// Package runtime 的中间件系统提供可插拔的 ReAct 循环监控能力。
//
// 中间件在每次迭代前后执行，可以检测循环、目标偏离、资源耗尽等异常，
// 并在必要时终止执行。中间件按注册顺序串行执行，任一中间件返回停止信号
// 则立即终止循环。
package runtime

import (
	"context"
	"fmt"
)

// IterationState 是单次迭代的状态快照，供中间件检查。
type IterationState struct {
	// 当前迭代号（从 1 开始）
	Iteration int

	// 当前思考内容（LLM 输出的文本部分）
	Thought string

	// 当前工具调用（nil 表示 LLM 返回最终答案）
	ToolCalls []*Action

	// 工具执行结果（上一次迭代的观察）
	LastObservation string

	// 最近 N 次工具名称（用于循环检测）
	RecentToolNames []string

	// 最近 N 次思考内容（用于思维循环检测）
	RecentThoughts []string

	// 连续工具失败次数
	ConsecutiveToolFailures int

	// 本次迭代是否成功
	IterationSucceeded bool

	// 累计生成的有效输出数量（如 observations, results）
	// 由业务层设置，中间件用于检测进展
	OutputCount int

	// 任务目标（初始 objective）
	Objective string

	// 自定义元数据（业务层可扩展）
	Metadata map[string]interface{}
}

// StopReason 是停止原因的枚举。
type StopReason string

const (
	// StopReasonToolLoop 工具调用循环
	StopReasonToolLoop StopReason = "tool_loop"

	// StopReasonThoughtLoop 思维内容循环
	StopReasonThoughtLoop StopReason = "thought_loop"

	// StopReasonNoProgress 长时间无进展
	StopReasonNoProgress StopReason = "no_progress"

	// StopReasonGoalDrift 偏离任务目标
	StopReasonGoalDrift StopReason = "goal_drift"

	// StopReasonResourceExhausted 资源耗尽
	StopReasonResourceExhausted StopReason = "resource_exhausted"

	// StopReasonCustom 自定义原因（业务层）
	StopReasonCustom StopReason = "custom"
)

// StopSignal 是中间件返回的停止信号。
type StopSignal struct {
	// 是否应该停止
	ShouldStop bool

	// 停止原因
	Reason StopReason

	// 详细说明（用于日志和调试）
	Message string
}

// Middleware 是 ReAct 循环的中间件接口。
//
// 中间件在每次迭代的关键点被调用，可以：
// 1. 检查异常模式（循环、偏离、资源耗尽）
// 2. 修改执行状态（如重置计数器）
// 3. 决定是否终止循环
//
// 中间件应该是无状态的，所有状态通过 IterationState 传递。
type Middleware interface {
	// Name 返回中间件名称（用于日志）
	Name() string

	// BeforeIteration 在每次迭代开始前调用。
	// 返回 StopSignal 表示是否应该停止循环。
	BeforeIteration(ctx context.Context, state *IterationState) (*StopSignal, error)

	// AfterIteration 在每次迭代完成后调用。
	// 返回 StopSignal 表示是否应该停止循环。
	AfterIteration(ctx context.Context, state *IterationState) (*StopSignal, error)
}

// MiddlewareChain 是中间件链，按顺序执行多个中间件。
type MiddlewareChain struct {
	middlewares []Middleware
}

// NewMiddlewareChain 创建中间件链。
func NewMiddlewareChain(middlewares ...Middleware) *MiddlewareChain {
	return &MiddlewareChain{
		middlewares: middlewares,
	}
}

// Add 添加中间件到链尾。
func (c *MiddlewareChain) Add(m Middleware) {
	c.middlewares = append(c.middlewares, m)
}

// BeforeIteration 依次调用所有中间件的 BeforeIteration。
// 一旦有中间件返回停止信号，立即停止并返回该信号。
func (c *MiddlewareChain) BeforeIteration(ctx context.Context, state *IterationState) (*StopSignal, error) {
	for _, m := range c.middlewares {
		signal, err := m.BeforeIteration(ctx, state)
		if err != nil {
			return nil, fmt.Errorf("中间件 %s BeforeIteration 失败: %w", m.Name(), err)
		}
		if signal != nil && signal.ShouldStop {
			return signal, nil
		}
	}
	return nil, nil
}

// AfterIteration 依次调用所有中间件的 AfterIteration。
// 一旦有中间件返回停止信号，立即停止并返回该信号。
func (c *MiddlewareChain) AfterIteration(ctx context.Context, state *IterationState) (*StopSignal, error) {
	for _, m := range c.middlewares {
		signal, err := m.AfterIteration(ctx, state)
		if err != nil {
			return nil, fmt.Errorf("中间件 %s AfterIteration 失败: %w", m.Name(), err)
		}
		if signal != nil && signal.ShouldStop {
			return signal, nil
		}
	}
	return nil, nil
}

// NoopMiddleware 是空操作中间件，用于测试或占位。
type NoopMiddleware struct{}

func (m *NoopMiddleware) Name() string {
	return "noop"
}

func (m *NoopMiddleware) BeforeIteration(ctx context.Context, state *IterationState) (*StopSignal, error) {
	return nil, nil
}

func (m *NoopMiddleware) AfterIteration(ctx context.Context, state *IterationState) (*StopSignal, error) {
	return nil, nil
}
