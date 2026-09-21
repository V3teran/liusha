package core

import (
	"context"
	"io"
)

// StreamEvent 流式事件
type StreamEvent struct {
	// 事件 ID（用于断点续传）
	ID string `json:"id,omitempty"`

	// 事件类型
	Type StreamEventType `json:"type"`

	// 事件数据
	Data any `json:"data"`

	// 事件时间戳（Unix 毫秒）
	Timestamp int64 `json:"timestamp"`

	// 是否为最后一个事件
	Done bool `json:"done"`
}

// StreamEventType 流式事件类型
type StreamEventType string

const (
	// StreamEventTypeMessage 消息事件（Agent 输出）
	StreamEventTypeMessage StreamEventType = "message"

	// StreamEventTypeThought 思考过程事件
	StreamEventTypeThought StreamEventType = "thought"

	// StreamEventTypeToolCall 工具调用事件
	StreamEventTypeToolCall StreamEventType = "tool_call"

	// StreamEventTypeToolResult 工具结果事件
	StreamEventTypeToolResult StreamEventType = "tool_result"

	// StreamEventTypeState 状态更新事件
	StreamEventTypeState StreamEventType = "state"

	// StreamEventTypeError 错误事件
	StreamEventTypeError StreamEventType = "error"

	// StreamEventTypeMetadata 元数据事件
	StreamEventTypeMetadata StreamEventType = "metadata"

	// StreamEventTypeHeartbeat 心跳事件（保持连接）
	StreamEventTypeHeartbeat StreamEventType = "heartbeat"

	// StreamEventTypeNodeStart 节点开始执行事件
	StreamEventTypeNodeStart StreamEventType = "node_start"

	// StreamEventTypeNodeEnd 节点执行完成事件
	StreamEventTypeNodeEnd StreamEventType = "node_end"
)

// StreamAdapter 流式输出适配器接口
// 将内部事件流转换为特定的传输格式（SSE、WebSocket 等）
type StreamAdapter interface {
	// Write 写入单个事件
	Write(ctx context.Context, event *StreamEvent) error

	// WriteMany 批量写入事件
	WriteMany(ctx context.Context, events []*StreamEvent) error

	// Flush 刷新缓冲区
	Flush() error

	// Close 关闭适配器
	Close() error

	// ContentType 返回适配器的 Content-Type
	ContentType() string
}

// StreamSource 流式数据源
// 用于从 Agent 执行过程中产生事件流
type StreamSource interface {
	// Subscribe 订阅事件流
	Subscribe(ctx context.Context) (<-chan *StreamEvent, error)

	// Unsubscribe 取消订阅
	Unsubscribe(subscriberID string) error
}

// StreamConfig 流式输出配置
type StreamConfig struct {
	// 启用流式输出
	Enabled bool

	// 缓冲区大小
	BufferSize int

	// 刷新间隔（毫秒）
	FlushInterval int64

	// 启用心跳（保持连接活跃）
	HeartbeatEnabled bool

	// 心跳间隔（毫秒）
	HeartbeatInterval int64

	// 最大重连次数
	MaxReconnects int

	// 重连间隔（毫秒）
	ReconnectInterval int64

	// 启用压缩
	CompressionEnabled bool
}

// DefaultStreamConfig 默认流式输出配置
func DefaultStreamConfig() *StreamConfig {
	return &StreamConfig{
		Enabled:            true,
		BufferSize:         100,
		FlushInterval:      100,  // 100ms
		HeartbeatEnabled:   true,
		HeartbeatInterval:  30000, // 30s
		MaxReconnects:      3,
		ReconnectInterval:  1000, // 1s
		CompressionEnabled: false,
	}
}

// StreamWriter 流式写入器
// 包装底层 Writer 并添加流式特性
type StreamWriter interface {
	io.Writer
	io.Closer

	// Flush 刷新缓冲区
	Flush() error
}

// BackpressureStrategy 背压策略
type BackpressureStrategy int

const (
	// BackpressureDrop 丢弃新事件
	BackpressureDrop BackpressureStrategy = iota

	// BackpressureBlock 阻塞等待
	BackpressureBlock

	// BackpressureBuffer 缓冲到磁盘
	BackpressureBuffer
)
