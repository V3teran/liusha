package middleware

import (
	"context"

	"github.com/V3teran/liusha/internal/framework/core"
)

// StreamProcessor 是流式处理器。
// 用于实时输出任务执行进度、LLM 生成内容等。
type StreamProcessor interface {
	// Stream 开始流式输出（返回事件通道）
	Stream(ctx context.Context, taskID string) (<-chan StreamEvent, error)

	// Send 发送流式事件
	Send(ctx context.Context, event StreamEvent) error

	// Close 关闭流
	Close(taskID string) error
}

// StreamEvent 是流式事件。
type StreamEvent struct {
	// 事件类型
	Type string `json:"type"`

	// 任务 ID
	TaskID string `json:"task_id"`

	// 数据（业务自定义）
	Data any `json:"data"`

	// 序列号（保证顺序）
	Sequence int64 `json:"sequence"`

	// 是否最后一个事件
	Final bool `json:"final"`
}

// StreamTransformer 是流式转换器。
type StreamTransformer interface {
	// Transform 转换事件
	Transform(ctx context.Context, event StreamEvent) (StreamEvent, error)

	// CanTransform 判断是否能转换此事件
	CanTransform(event StreamEvent) bool
}

// StreamAggregator 是流式聚合器。
// 将多个流合并为一个流。
type StreamAggregator interface {
	// Aggregate 聚合多个流
	Aggregate(ctx context.Context, streams []<-chan StreamEvent) (<-chan StreamEvent, error)

	// Merge 合并多个事件
	Merge(events []StreamEvent) (StreamEvent, error)
}

// StreamBuffer 是流式缓冲器。
// 缓存流式事件，防止消费者太慢导致阻塞。
type StreamBuffer interface {
	// Buffer 缓冲流
	Buffer(ctx context.Context, stream <-chan StreamEvent, bufferSize int) (<-chan StreamEvent, error)

	// Flush 刷新缓冲区
	Flush(ctx context.Context) error
}

// StreamRateLimiter 是流式限流器。
type StreamRateLimiter interface {
	// Limit 限流（控制流速）
	Limit(ctx context.Context, stream <-chan StreamEvent, ratePerSec int) (<-chan StreamEvent, error)
}

// StreamRecorder 是流式记录器。
// 将流式事件持久化，支持回放。
type StreamRecorder interface {
	// Record 记录流
	Record(ctx context.Context, taskID string, stream <-chan StreamEvent) error

	// Replay 回放流
	Replay(ctx context.Context, taskID string) (<-chan StreamEvent, error)
}

// LLMStreamEvent 是 LLM 流式输出事件。
type LLMStreamEvent struct {
	// 增量内容
	Delta string `json:"delta"`

	// 累积内容
	Accumulated string `json:"accumulated"`

	// Token 数量
	TokenCount int `json:"token_count"`

	// 完成原因：stop, length, tool_call, error
	FinishReason string `json:"finish_reason,omitempty"`

	// 工具调用（如果有）
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

// ToolCall 是工具调用信息。
type ToolCall struct {
	// 工具名称
	Name string `json:"name"`

	// 工具参数（JSON）
	Arguments string `json:"arguments"`

	// 调用 ID
	ID string `json:"id"`
}

// ProgressEvent 是进度事件。
type ProgressEvent struct {
	// 当前阶段
	Phase string `json:"phase"`

	// 进度百分比（0-100）
	Progress int `json:"progress"`

	// 当前步骤描述
	Step string `json:"step"`

	// 已完成的节点数
	CompletedNodes int `json:"completed_nodes"`

	// 总节点数
	TotalNodes int `json:"total_nodes"`

	// 预计剩余时间（毫秒）
	EstimatedRemainingMs int64 `json:"estimated_remaining_ms,omitempty"`
}

// LogEvent 是日志事件。
type LogEvent struct {
	// 日志级别：debug, info, warn, error
	Level string `json:"level"`

	// 日志消息
	Message string `json:"message"`

	// 日志来源
	Source string `json:"source"`

	// 时间戳（Unix 毫秒）
	Timestamp int64 `json:"timestamp"`

	// 额外字段
	Fields map[string]any `json:"fields,omitempty"`
}

// SSETransport 是 SSE（Server-Sent Events）传输层。
type SSETransport interface {
	// Send 发送 SSE 事件
	Send(ctx context.Context, event StreamEvent) error

	// Close 关闭连接
	Close() error
}

// WebSocketTransport 是 WebSocket 传输层。
type WebSocketTransport interface {
	// Send 发送消息
	Send(ctx context.Context, event StreamEvent) error

	// Receive 接收消息
	Receive(ctx context.Context) (StreamEvent, error)

	// CloseConn 关闭连接
	CloseConn() error
}

// StreamEventBus 是流式事件总线。
// 整合 EventBus 和 StreamProcessor。
type StreamEventBus interface {
	// 事件发布订阅
	core.EventPublisher
	core.EventSubscriber

	// 流式处理
	Stream(ctx context.Context, taskID string) (<-chan StreamEvent, error)
	Send(ctx context.Context, event StreamEvent) error

	// 转换函数
	ConvertToStream(event core.Event) StreamEvent
	ConvertToEvent(streamEvent StreamEvent) core.Event

	// 关闭总线
	Close() error
}
