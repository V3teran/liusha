package core

import (
	"context"
	"time"

	"github.com/V3teran/liusha/internal/framework/llm"
)

// Callback 是统一的回调接口，用于横切关注点（日志、监控、成本追踪）。
//
// 设计原则：
// - 所有方法都不应阻塞主流程（快速返回）
// - 错误不应传播到主流程（内部处理）
// - 支持多个 Callback 链式调用
type Callback interface {
	// OnAgentStart Agent 启动前
	OnAgentStart(ctx context.Context, event AgentStartEvent)

	// OnAgentEnd Agent 结束后
	OnAgentEnd(ctx context.Context, event AgentEndEvent)

	// OnToolStart Tool 执行前
	OnToolStart(ctx context.Context, event ToolStartEvent)

	// OnToolEnd Tool 执行后
	OnToolEnd(ctx context.Context, event ToolEndEvent)

	// OnLLMStart LLM 调用前
	OnLLMStart(ctx context.Context, event LLMStartEvent)

	// OnLLMEnd LLM 调用后
	OnLLMEnd(ctx context.Context, event LLMEndEvent)
}

// ─────────────────────────────────────────────
//  事件类型定义
// ─────────────────────────────────────────────

// AgentStartEvent Agent 启动事件
type AgentStartEvent struct {
	AgentName string
	TaskID    string
	StartTime time.Time
	Metadata  map[string]any
}

// AgentEndEvent Agent 结束事件
type AgentEndEvent struct {
	AgentName string
	TaskID    string
	StartTime time.Time
	EndTime   time.Time
	Duration  time.Duration
	Error     error
	Metadata  map[string]any
}

// ToolStartEvent Tool 执行前事件
type ToolStartEvent struct {
	ToolName  string
	TaskID    string
	Input     any
	StartTime time.Time
	Metadata  map[string]any
}

// ToolEndEvent Tool 执行后事件
type ToolEndEvent struct {
	ToolName  string
	TaskID    string
	Input     any
	Output    any
	StartTime time.Time
	EndTime   time.Time
	Duration  time.Duration
	Error     error
	Metadata  map[string]any
}

// LLMStartEvent LLM 调用前事件
type LLMStartEvent struct {
	ProviderID string
	ModelID    string
	TaskID     string
	Request    llm.Request
	StartTime  time.Time
	Metadata   map[string]any
}

// LLMEndEvent LLM 调用后事件
type LLMEndEvent struct {
	ProviderID string
	ModelID    string
	TaskID     string
	Request    llm.Request
	Response   llm.Response
	StartTime  time.Time
	EndTime    time.Time
	Duration   time.Duration
	Error      error
	Metadata   map[string]any
}

// 注意：NodeStartEvent 和 NodeEndEvent 已删除
// 因为依赖已删除的 Graph/Node 类型，且业务层 0 使用

// ─────────────────────────────────────────────
//  CallbackChain - 链式调用多个 Callback
// ─────────────────────────────────────────────

// CallbackChain 是多个 Callback 的组合。
type CallbackChain struct {
	callbacks []Callback
}

// NewCallbackChain 创建 Callback 链。
func NewCallbackChain(callbacks ...Callback) *CallbackChain {
	return &CallbackChain{
		callbacks: callbacks,
	}
}

// Add 添加 Callback 到链。
func (c *CallbackChain) Add(callback Callback) {
	c.callbacks = append(c.callbacks, callback)
}

// OnAgentStart 实现 Callback 接口
func (c *CallbackChain) OnAgentStart(ctx context.Context, event AgentStartEvent) {
	for _, cb := range c.callbacks {
		cb.OnAgentStart(ctx, event)
	}
}

// OnAgentEnd 实现 Callback 接口
func (c *CallbackChain) OnAgentEnd(ctx context.Context, event AgentEndEvent) {
	for _, cb := range c.callbacks {
		cb.OnAgentEnd(ctx, event)
	}
}

// OnToolStart 实现 Callback 接口
func (c *CallbackChain) OnToolStart(ctx context.Context, event ToolStartEvent) {
	for _, cb := range c.callbacks {
		cb.OnToolStart(ctx, event)
	}
}

// OnToolEnd 实现 Callback 接口
func (c *CallbackChain) OnToolEnd(ctx context.Context, event ToolEndEvent) {
	for _, cb := range c.callbacks {
		cb.OnToolEnd(ctx, event)
	}
}

// OnLLMStart 实现 Callback 接口
func (c *CallbackChain) OnLLMStart(ctx context.Context, event LLMStartEvent) {
	for _, cb := range c.callbacks {
		cb.OnLLMStart(ctx, event)
	}
}

// OnLLMEnd 实现 Callback 接口
func (c *CallbackChain) OnLLMEnd(ctx context.Context, event LLMEndEvent) {
	for _, cb := range c.callbacks {
		cb.OnLLMEnd(ctx, event)
	}
}
// ─────────────────────────────────────────────
//  NoopCallback - 空实现（用于嵌入）
// ─────────────────────────────────────────────

// NoopCallback 是 Callback 的空实现。
// 业务可以嵌入此类型，只实现关心的方法。
type NoopCallback struct{}

func (n NoopCallback) OnAgentStart(ctx context.Context, event AgentStartEvent) {}
func (n NoopCallback) OnAgentEnd(ctx context.Context, event AgentEndEvent)     {}
func (n NoopCallback) OnToolStart(ctx context.Context, event ToolStartEvent)   {}
func (n NoopCallback) OnToolEnd(ctx context.Context, event ToolEndEvent)       {}
func (n NoopCallback) OnLLMStart(ctx context.Context, event LLMStartEvent)     {}
func (n NoopCallback) OnLLMEnd(ctx context.Context, event LLMEndEvent)         {}
