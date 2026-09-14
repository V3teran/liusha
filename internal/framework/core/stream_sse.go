package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"
)

// SSEAdapter Server-Sent Events 适配器
// 实现 SSE 协议的流式输出
type SSEAdapter struct {
	writer io.Writer
	mu     sync.Mutex
	config *StreamConfig
	closed bool

	// 心跳定时器
	heartbeatTicker *time.Ticker
	heartbeatDone   chan bool
}

// NewSSEAdapter 创建 SSE 适配器
func NewSSEAdapter(writer io.Writer, config *StreamConfig) *SSEAdapter {
	if config == nil {
		config = DefaultStreamConfig()
	}

	adapter := &SSEAdapter{
		writer:        writer,
		config:        config,
		closed:        false,
		heartbeatDone: make(chan bool),
	}

	// 启动心跳
	if config.HeartbeatEnabled {
		adapter.startHeartbeat()
	}

	return adapter
}

// Write 写入单个事件
func (a *SSEAdapter) Write(ctx context.Context, event *StreamEvent) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.closed {
		return fmt.Errorf("SSE 适配器已关闭")
	}

	// 序列化事件数据
	dataBytes, err := json.Marshal(event.Data)
	if err != nil {
		return fmt.Errorf("序列化事件数据失败: %w", err)
	}

	// 构造 SSE 格式
	// SSE 格式：
	// id: <event-id>
	// event: <event-type>
	// data: <json-data>
	// (空行)

	var output string

	// ID 字段（可选，用于断点续传）
	if event.ID != "" {
		output += fmt.Sprintf("id: %s\n", event.ID)
	}

	// Event 类型
	output += fmt.Sprintf("event: %s\n", event.Type)

	// Data 字段（支持多行）
	output += fmt.Sprintf("data: %s\n", string(dataBytes))

	// 结束标记（空行）
	output += "\n"

	// 写入底层 Writer
	_, err = a.writer.Write([]byte(output))
	if err != nil {
		return fmt.Errorf("写入 SSE 数据失败: %w", err)
	}

	return nil
}

// WriteMany 批量写入事件
func (a *SSEAdapter) WriteMany(ctx context.Context, events []*StreamEvent) error {
	for _, event := range events {
		if err := a.Write(ctx, event); err != nil {
			return err
		}
	}
	return a.Flush()
}

// Flush 刷新缓冲区
func (a *SSEAdapter) Flush() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.closed {
		return fmt.Errorf("SSE 适配器已关闭")
	}

	// 如果 writer 实现了 Flusher 接口，调用 Flush
	if flusher, ok := a.writer.(interface{ Flush() error }); ok {
		return flusher.Flush()
	}

	// 如果是 http.ResponseWriter，使用类型断言
	if flusher, ok := a.writer.(interface{ Flush() }); ok {
		flusher.Flush()
	}

	return nil
}

// Close 关闭适配器
func (a *SSEAdapter) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.closed {
		return nil
	}

	a.closed = true

	// 停止心跳
	if a.heartbeatTicker != nil {
		a.heartbeatTicker.Stop()
		close(a.heartbeatDone)
	}

	return nil
}

// ContentType 返回 SSE 的 Content-Type
func (a *SSEAdapter) ContentType() string {
	return "text/event-stream"
}

// startHeartbeat 启动心跳发送
func (a *SSEAdapter) startHeartbeat() {
	interval := time.Duration(a.config.HeartbeatInterval) * time.Millisecond
	a.heartbeatTicker = time.NewTicker(interval)

	go func() {
		for {
			select {
			case <-a.heartbeatTicker.C:
				// 发送心跳事件
				event := &StreamEvent{
					Type:      StreamEventTypeHeartbeat,
					Data:      map[string]any{"ping": time.Now().UnixMilli()},
					Timestamp: time.Now().UnixMilli(),
				}
				_ = a.Write(context.Background(), event)
				_ = a.Flush()

			case <-a.heartbeatDone:
				return
			}
		}
	}()
}

// SSEComment 写入 SSE 注释（用于保持连接或调试）
func (a *SSEAdapter) SSEComment(comment string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.closed {
		return fmt.Errorf("SSE 适配器已关闭")
	}

	// SSE 注释格式: : <comment>\n
	output := fmt.Sprintf(": %s\n\n", comment)
	_, err := a.writer.Write([]byte(output))
	return err
}

// SSERetry 设置客户端重连间隔（毫秒）
func (a *SSEAdapter) SSERetry(milliseconds int) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.closed {
		return fmt.Errorf("SSE 适配器已关闭")
	}

	// SSE retry 格式: retry: <milliseconds>\n
	output := fmt.Sprintf("retry: %d\n\n", milliseconds)
	_, err := a.writer.Write([]byte(output))
	return err
}
