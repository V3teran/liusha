package core

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSSEAdapter_Basic 测试 SSE 适配器基础功能
func TestSSEAdapter_Basic(t *testing.T) {
	ctx := context.Background()

	t.Run("写入单个事件", func(t *testing.T) {
		buf := &bytes.Buffer{}
		config := DefaultStreamConfig()
		config.HeartbeatEnabled = false // 禁用心跳
		adapter := NewSSEAdapter(buf, config)
		defer adapter.Close()

		event := &StreamEvent{
			ID:        "event-1",
			Type:      StreamEventTypeMessage,
			Data:      map[string]any{"text": "Hello SSE"},
			Timestamp: 1234567890,
		}

		err := adapter.Write(ctx, event)
		require.NoError(t, err)

		output := buf.String()
		assert.Contains(t, output, "id: event-1")
		assert.Contains(t, output, "event: message")
		assert.Contains(t, output, `"text":"Hello SSE"`)
	})

	t.Run("批量写入事件", func(t *testing.T) {
		buf := &bytes.Buffer{}
		config := DefaultStreamConfig()
		config.HeartbeatEnabled = false
		adapter := NewSSEAdapter(buf, config)
		defer adapter.Close()

		events := []*StreamEvent{
			{Type: StreamEventTypeMessage, Data: map[string]any{"msg": "1"}},
			{Type: StreamEventTypeMessage, Data: map[string]any{"msg": "2"}},
			{Type: StreamEventTypeMessage, Data: map[string]any{"msg": "3"}},
		}

		err := adapter.WriteMany(ctx, events)
		require.NoError(t, err)

		output := buf.String()
		assert.Contains(t, output, `"msg":"1"`)
		assert.Contains(t, output, `"msg":"2"`)
		assert.Contains(t, output, `"msg":"3"`)
	})

	t.Run("ContentType", func(t *testing.T) {
		buf := &bytes.Buffer{}
		adapter := NewSSEAdapter(buf, nil)
		defer adapter.Close()

		assert.Equal(t, "text/event-stream", adapter.ContentType())
	})

	t.Run("SSE 注释", func(t *testing.T) {
		buf := &bytes.Buffer{}
		config := DefaultStreamConfig()
		config.HeartbeatEnabled = false
		adapter := NewSSEAdapter(buf, config)
		defer adapter.Close()

		err := adapter.SSEComment("This is a comment")
		require.NoError(t, err)

		output := buf.String()
		assert.Contains(t, output, ": This is a comment")
	})

	t.Run("SSE Retry", func(t *testing.T) {
		buf := &bytes.Buffer{}
		config := DefaultStreamConfig()
		config.HeartbeatEnabled = false
		adapter := NewSSEAdapter(buf, config)
		defer adapter.Close()

		err := adapter.SSERetry(3000)
		require.NoError(t, err)

		output := buf.String()
		assert.Contains(t, output, "retry: 3000")
	})

	t.Run("关闭后无法写入", func(t *testing.T) {
		buf := &bytes.Buffer{}
		config := DefaultStreamConfig()
		config.HeartbeatEnabled = false
		adapter := NewSSEAdapter(buf, config)

		adapter.Close()

		event := &StreamEvent{Type: StreamEventTypeMessage, Data: "test"}
		err := adapter.Write(ctx, event)
		require.Error(t, err)
	})
}

// TestSSEAdapter_Heartbeat 测试心跳功能
func TestSSEAdapter_Heartbeat(t *testing.T) {
	t.Run("启用心跳", func(t *testing.T) {
		buf := &bytes.Buffer{}
		config := DefaultStreamConfig()
		config.HeartbeatEnabled = true
		config.HeartbeatInterval = 100 // 100ms

		adapter := NewSSEAdapter(buf, config)
		defer adapter.Close()

		// 等待心跳发送
		time.Sleep(250 * time.Millisecond)

		output := buf.String()
		// 应该至少有 2 个心跳事件
		count := strings.Count(output, "event: heartbeat")
		assert.GreaterOrEqual(t, count, 2)
	})
}

// TestEventSource_Basic 测试事件源基础功能
func TestEventSource_Basic(t *testing.T) {
	ctx := context.Background()

	t.Run("订阅和接收事件", func(t *testing.T) {
		source := NewEventSource(nil)
		defer source.Close()

		// 订阅
		eventChan, err := source.Subscribe(ctx)
		require.NoError(t, err)

		// 发送事件
		event := &StreamEvent{
			Type: StreamEventTypeMessage,
			Data: map[string]any{"text": "Hello"},
		}

		err = source.Emit(event)
		require.NoError(t, err)

		// 接收事件
		select {
		case received := <-eventChan:
			assert.Equal(t, StreamEventTypeMessage, received.Type)
			assert.Equal(t, "Hello", received.Data.(map[string]any)["text"])
		case <-time.After(time.Second):
			t.Fatal("未收到事件")
		}
	})

	t.Run("多个订阅者", func(t *testing.T) {
		source := NewEventSource(nil)
		defer source.Close()

		// 创建 3 个订阅者
		channels := make([]<-chan *StreamEvent, 3)
		for i := 0; i < 3; i++ {
			ch, err := source.Subscribe(ctx)
			require.NoError(t, err)
			channels[i] = ch
		}

		// 发送事件
		event := &StreamEvent{
			Type: StreamEventTypeMessage,
			Data: "broadcast",
		}
		err := source.Emit(event)
		require.NoError(t, err)

		// 所有订阅者都应该收到
		for i, ch := range channels {
			select {
			case received := <-ch:
				assert.Equal(t, "broadcast", received.Data)
			case <-time.After(time.Second):
				t.Fatalf("订阅者 %d 未收到事件", i)
			}
		}
	})

	t.Run("批量发送事件", func(t *testing.T) {
		source := NewEventSource(nil)
		defer source.Close()

		eventChan, _ := source.Subscribe(ctx)

		// 批量发送
		events := []*StreamEvent{
			{Type: StreamEventTypeMessage, Data: "1"},
			{Type: StreamEventTypeMessage, Data: "2"},
			{Type: StreamEventTypeMessage, Data: "3"},
		}

		err := source.EmitMany(events)
		require.NoError(t, err)

		// 接收所有事件
		for i := 0; i < 3; i++ {
			select {
			case <-eventChan:
				// 收到事件
			case <-time.After(time.Second):
				t.Fatalf("未收到第 %d 个事件", i+1)
			}
		}
	})

	t.Run("订阅者数量", func(t *testing.T) {
		source := NewEventSource(nil)
		defer source.Close()

		assert.Equal(t, 0, source.SubscriberCount())

		_, _ = source.Subscribe(ctx)
		assert.Equal(t, 1, source.SubscriberCount())

		_, _ = source.Subscribe(ctx)
		assert.Equal(t, 2, source.SubscriberCount())
	})
}

// TestEventSource_Filter 测试事件过滤
func TestEventSource_Filter(t *testing.T) {
	ctx := context.Background()

	t.Run("按类型过滤", func(t *testing.T) {
		source := NewEventSource(nil)
		defer source.Close()

		// 只订阅 message 和 error 类型
		filter := FilterByType(StreamEventTypeMessage, StreamEventTypeError)
		eventChan, err := source.SubscribeWithFilter(ctx, filter)
		require.NoError(t, err)

		// 发送多种类型的事件
		events := []*StreamEvent{
			{Type: StreamEventTypeMessage, Data: "msg1"},
			{Type: StreamEventTypeThought, Data: "thought1"}, // 应该被过滤
			{Type: StreamEventTypeError, Data: "error1"},
			{Type: StreamEventTypeToolCall, Data: "tool1"}, // 应该被过滤
		}

		for _, e := range events {
			_ = source.Emit(e)
		}

		// 应该只收到 2 个事件
		received := 0
		timeout := time.After(500 * time.Millisecond)
		for received < 2 {
			select {
			case event := <-eventChan:
				received++
				assert.True(t,
					event.Type == StreamEventTypeMessage || event.Type == StreamEventTypeError,
					"收到了不应该通过过滤器的事件",
				)
			case <-timeout:
				break
			}
		}

		assert.Equal(t, 2, received, "应该收到 2 个事件")
	})

	t.Run("组合过滤器 AND", func(t *testing.T) {
		source := NewEventSource(nil)
		defer source.Close()

		// 同时满足两个条件：类型为 message 且有特定 ID
		filter := CombineFilters(
			FilterByType(StreamEventTypeMessage),
			FilterByID("event-1", "event-2"),
		)

		eventChan, _ := source.SubscribeWithFilter(ctx, filter)

		events := []*StreamEvent{
			{ID: "event-1", Type: StreamEventTypeMessage, Data: "pass1"}, // 通过
			{ID: "event-1", Type: StreamEventTypeError, Data: "fail1"},   // 类型不匹配
			{ID: "event-3", Type: StreamEventTypeMessage, Data: "fail2"}, // ID 不匹配
			{ID: "event-2", Type: StreamEventTypeMessage, Data: "pass2"}, // 通过
		}

		for _, e := range events {
			_ = source.Emit(e)
		}

		// 应该只收到 2 个事件
		received := 0
		timeout := time.After(500 * time.Millisecond)
		for received < 2 {
			select {
			case <-eventChan:
				received++
			case <-timeout:
				break
			}
		}

		assert.Equal(t, 2, received)
	})

	t.Run("组合过滤器 OR", func(t *testing.T) {
		source := NewEventSource(nil)
		defer source.Close()

		// 满足任一条件：message 类型或 error 类型
		filter := AnyFilter(
			FilterByType(StreamEventTypeMessage),
			FilterByType(StreamEventTypeError),
		)

		eventChan, _ := source.SubscribeWithFilter(ctx, filter)

		events := []*StreamEvent{
			{Type: StreamEventTypeMessage, Data: "msg"}, // 通过
			{Type: StreamEventTypeThought, Data: "th"},  // 不通过
			{Type: StreamEventTypeError, Data: "err"},   // 通过
		}

		for _, e := range events {
			_ = source.Emit(e)
		}

		received := 0
		timeout := time.After(500 * time.Millisecond)
		for received < 2 {
			select {
			case <-eventChan:
				received++
			case <-timeout:
				break
			}
		}

		assert.Equal(t, 2, received)
	})
}

// TestStreamIntegration 测试完整的流式输出集成
func TestStreamIntegration(t *testing.T) {
	ctx := context.Background()

	t.Run("EventSource -> SSEAdapter", func(t *testing.T) {
		// 创建事件源
		source := NewEventSource(nil)
		defer source.Close()

		// 订阅事件
		eventChan, err := source.Subscribe(ctx)
		require.NoError(t, err)

		// 创建 SSE 适配器
		buf := &bytes.Buffer{}
		config := DefaultStreamConfig()
		config.HeartbeatEnabled = false
		adapter := NewSSEAdapter(buf, config)
		defer adapter.Close()

		// 启动转发 goroutine
		done := make(chan bool)
		go func() {
			for event := range eventChan {
				_ = adapter.Write(ctx, event)
			}
			done <- true
		}()

		// 发送事件
		events := []*StreamEvent{
			{Type: StreamEventTypeMessage, Data: map[string]any{"step": 1}},
			{Type: StreamEventTypeMessage, Data: map[string]any{"step": 2}},
			{Type: StreamEventTypeMessage, Data: map[string]any{"step": 3}},
		}

		for _, e := range events {
			_ = source.Emit(e)
		}

		// 等待转发完成
		time.Sleep(100 * time.Millisecond)
		source.Close()
		<-done

		// 验证输出
		output := buf.String()
		assert.Contains(t, output, `"step":1`)
		assert.Contains(t, output, `"step":2`)
		assert.Contains(t, output, `"step":3`)
	})
}

// BenchmarkStreamAdapter 流式适配器性能基准
func BenchmarkStreamAdapter(b *testing.B) {
	ctx := context.Background()

	b.Run("SSEAdapter_Write", func(b *testing.B) {
		buf := &bytes.Buffer{}
		config := DefaultStreamConfig()
		config.HeartbeatEnabled = false
		adapter := NewSSEAdapter(buf, config)
		defer adapter.Close()

		event := &StreamEvent{
			Type: StreamEventTypeMessage,
			Data: map[string]any{"benchmark": true},
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = adapter.Write(ctx, event)
		}
	})

	b.Run("EventSource_Emit", func(b *testing.B) {
		source := NewEventSource(nil)
		defer source.Close()
		_, _ = source.Subscribe(ctx)

		event := &StreamEvent{
			Type: StreamEventTypeMessage,
			Data: "benchmark",
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = source.Emit(event)
		}
	})
}
