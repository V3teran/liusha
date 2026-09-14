package core

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// WebSocketAdapter WebSocket 适配器
// 实现基于 WebSocket 的双向流式通信
type WebSocketAdapter struct {
	conn   *websocket.Conn
	mu     sync.Mutex
	config *StreamConfig
	closed bool

	// 心跳定时器
	heartbeatTicker *time.Ticker
	heartbeatDone   chan bool

	// 写入队列（用于背压控制）
	writeQueue chan *StreamEvent
	queueDone  chan bool
}

// NewWebSocketAdapter 创建 WebSocket 适配器
func NewWebSocketAdapter(conn *websocket.Conn, config *StreamConfig) *WebSocketAdapter {
	if config == nil {
		config = DefaultStreamConfig()
	}

	adapter := &WebSocketAdapter{
		conn:          conn,
		config:        config,
		closed:        false,
		heartbeatDone: make(chan bool),
		writeQueue:    make(chan *StreamEvent, config.BufferSize),
		queueDone:     make(chan bool),
	}

	// 启动写入队列处理器
	go adapter.processWriteQueue()

	// 启动心跳
	if config.HeartbeatEnabled {
		adapter.startHeartbeat()
	}

	return adapter
}

// Write 写入单个事件
func (a *WebSocketAdapter) Write(ctx context.Context, event *StreamEvent) error {
	if a.closed {
		return fmt.Errorf("WebSocket 适配器已关闭")
	}

	// 将事件放入写入队列
	select {
	case a.writeQueue <- event:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	default:
		// 队列满，根据背压策略处理
		return fmt.Errorf("写入队列已满，事件被丢弃")
	}
}

// WriteMany 批量写入事件
func (a *WebSocketAdapter) WriteMany(ctx context.Context, events []*StreamEvent) error {
	for _, event := range events {
		if err := a.Write(ctx, event); err != nil {
			return err
		}
	}
	return nil
}

// Flush 刷新缓冲区（WebSocket 立即发送，无需显式刷新）
func (a *WebSocketAdapter) Flush() error {
	return nil
}

// Close 关闭适配器
func (a *WebSocketAdapter) Close() error {
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

	// 停止写入队列
	close(a.queueDone)
	close(a.writeQueue)

	// 发送关闭消息
	closeMessage := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "连接正常关闭")
	_ = a.conn.WriteControl(websocket.CloseMessage, closeMessage, time.Now().Add(time.Second))

	// 关闭连接
	return a.conn.Close()
}

// ContentType 返回 WebSocket 的 Content-Type
func (a *WebSocketAdapter) ContentType() string {
	return "application/json"
}

// processWriteQueue 处理写入队列
func (a *WebSocketAdapter) processWriteQueue() {
	for {
		select {
		case event, ok := <-a.writeQueue:
			if !ok {
				// 队列已关闭
				return
			}

			// 写入 WebSocket
			if err := a.writeEvent(event); err != nil {
				// 写入失败，记录错误（实际应用中应该有日志系统）
				continue
			}

		case <-a.queueDone:
			return
		}
	}
}

// writeEvent 写入单个事件到 WebSocket
func (a *WebSocketAdapter) writeEvent(event *StreamEvent) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.closed {
		return fmt.Errorf("WebSocket 适配器已关闭")
	}

	// 序列化事件
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("序列化事件失败: %w", err)
	}

	// 设置写入超时
	_ = a.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))

	// 写入 JSON 消息
	err = a.conn.WriteMessage(websocket.TextMessage, data)
	if err != nil {
		return fmt.Errorf("写入 WebSocket 消息失败: %w", err)
	}

	return nil
}

// startHeartbeat 启动心跳发送
func (a *WebSocketAdapter) startHeartbeat() {
	interval := time.Duration(a.config.HeartbeatInterval) * time.Millisecond
	a.heartbeatTicker = time.NewTicker(interval)

	go func() {
		for {
			select {
			case <-a.heartbeatTicker.C:
				// 发送 Ping 帧
				a.mu.Lock()
				if !a.closed {
					_ = a.conn.SetWriteDeadline(time.Now().Add(time.Second))
					_ = a.conn.WriteMessage(websocket.PingMessage, []byte{})
				}
				a.mu.Unlock()

			case <-a.heartbeatDone:
				return
			}
		}
	}()

	// 设置 Pong 处理器
	a.conn.SetPongHandler(func(appData string) error {
		// 收到 Pong，连接正常
		return nil
	})
}

// ReadMessage 读取客户端消息（双向通信）
func (a *WebSocketAdapter) ReadMessage(ctx context.Context) (*StreamEvent, error) {
	if a.closed {
		return nil, fmt.Errorf("WebSocket 适配器已关闭")
	}

	// 读取消息
	_, message, err := a.conn.ReadMessage()
	if err != nil {
		return nil, fmt.Errorf("读取 WebSocket 消息失败: %w", err)
	}

	// 解析事件
	var event StreamEvent
	if err := json.Unmarshal(message, &event); err != nil {
		return nil, fmt.Errorf("解析事件失败: %w", err)
	}

	return &event, nil
}

// SetReadDeadline 设置读取超时
func (a *WebSocketAdapter) SetReadDeadline(t time.Time) error {
	return a.conn.SetReadDeadline(t)
}

// SetWriteDeadline 设置写入超时
func (a *WebSocketAdapter) SetWriteDeadline(t time.Time) error {
	return a.conn.SetWriteDeadline(t)
}
