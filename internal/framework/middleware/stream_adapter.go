package middleware

import (
	"context"
	"sync"

	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/framework/llm"
)

// StreamAdapter 是流式事件适配器，负责在不同层级的事件类型之间转换。
//
// 转换链：
// llm.StreamEvent (Provider 层) → middleware.StreamEvent (业务层 → 前端)
type StreamAdapter struct {
	taskID   string
	sequence int64
	mu       sync.Mutex
}

// NewStreamAdapter 创建流式适配器。
func NewStreamAdapter(taskID string) *StreamAdapter {
	return &StreamAdapter{
		taskID:   taskID,
		sequence: 0,
	}
}

// ConvertLLMStream 将 LLM Provider 层的流式事件转换为业务层事件。
func (a *StreamAdapter) ConvertLLMStream(llmEvent llm.StreamEvent) StreamEvent {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.sequence++

	event := StreamEvent{
		TaskID:   a.taskID,
		Sequence: a.sequence,
	}

	switch llmEvent.Kind {
	case llm.StreamThinking:
		// Thinking 内容（Anthropic 专有）
		event.Type = "llm.thinking"
		event.Data = LLMStreamEvent{
			Delta:       llmEvent.Content,
			Accumulated: "", // Thinking 不累积
		}
		event.Final = false

	case llm.StreamText:
		// 文本增量
		event.Type = "llm.text"
		event.Data = LLMStreamEvent{
			Delta: llmEvent.Content,
		}
		event.Final = false

	case llm.StreamToolCall:
		// 工具调用
		event.Type = "llm.tool_call"
		event.Data = LLMStreamEvent{
			ToolCalls: []ToolCall{
				{
					ID:        llmEvent.Tool.ID,
					Name:      llmEvent.Tool.Name,
					Arguments: string(llmEvent.Tool.Arguments),
				},
			},
		}
		event.Final = false

	case llm.StreamDone:
		// 流结束
		event.Type = "llm.done"
		if llmEvent.Usage != nil {
			event.Data = map[string]any{
				"usage": map[string]int{
					"in_tokens":     llmEvent.Usage.InTokens,
					"out_tokens":    llmEvent.Usage.OutTokens,
					"cached_tokens": llmEvent.Usage.CachedTokens,
				},
			}
		}
		event.Final = true

	case llm.StreamError:
		// 错误
		event.Type = "llm.error"
		event.Data = map[string]any{
			"error": llmEvent.Err.Error(),
		}
		event.Final = true
	}

	return event
}

// ConvertProgressEvent 将进度事件转换为统一格式。
func (a *StreamAdapter) ConvertProgressEvent(progress ProgressEvent) StreamEvent {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.sequence++

	return StreamEvent{
		Type:     "progress",
		TaskID:   a.taskID,
		Data:     progress,
		Sequence: a.sequence,
		Final:    progress.Progress >= 100,
	}
}

// ConvertLogEvent 将日志事件转换为统一格式。
func (a *StreamAdapter) ConvertLogEvent(log LogEvent) StreamEvent {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.sequence++

	return StreamEvent{
		Type:     "log",
		TaskID:   a.taskID,
		Data:     log,
		Sequence: a.sequence,
		Final:    false,
	}
}

// ─────────────────────────────────────────────
//  StreamEventBus - 流式事件总线实现
// ─────────────────────────────────────────────

// StreamEventBusImpl 是流式事件总线的实现。
type StreamEventBusImpl struct {
	eventBus core.EventBus
	adapters sync.Map // taskID -> *StreamAdapter
	channels sync.Map // taskID -> chan StreamEvent
	mu       sync.RWMutex
}

// NewStreamEventBus 创建流式事件总线。
func NewStreamEventBus(eventBus core.EventBus) *StreamEventBusImpl {
	return &StreamEventBusImpl{
		eventBus: eventBus,
	}
}

// Publish 发布事件到底层 EventBus。
func (b *StreamEventBusImpl) Publish(event core.Event) {
	if b.eventBus != nil {
		b.eventBus.Publish(event)
	}
}

// Subscribe 订阅事件（适配 EventBus 为流式接口）。
func (b *StreamEventBusImpl) Subscribe(ctx context.Context, actionID string) (<-chan core.Event, error) {
	if b.eventBus == nil {
		ch := make(chan core.Event)
		close(ch)
		return ch, nil
	}

	sub := b.eventBus.Subscribe(ctx, actionID)
	return sub.Events(), nil
}

// Stream 创建或获取指定任务的流式通道。
func (b *StreamEventBusImpl) Stream(ctx context.Context, taskID string) (<-chan StreamEvent, error) {
	// 检查是否已存在通道
	if ch, ok := b.channels.Load(taskID); ok {
		return ch.(chan StreamEvent), nil
	}

	// 创建新通道
	ch := make(chan StreamEvent, 100) // 缓冲 100 个事件
	b.channels.Store(taskID, ch)

	// 创建适配器
	adapter := NewStreamAdapter(taskID)
	b.adapters.Store(taskID, adapter)

	return ch, nil
}

// Send 发送流式事件到指定任务的通道。
func (b *StreamEventBusImpl) Send(ctx context.Context, event StreamEvent) error {
	ch, ok := b.channels.Load(event.TaskID)
	if !ok {
		// 任务通道不存在，忽略
		return nil
	}

	select {
	case ch.(chan StreamEvent) <- event:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ConvertToStream 将 core.Event 转换为 StreamEvent（保留兼容性）。
func (b *StreamEventBusImpl) ConvertToStream(event core.Event) StreamEvent {
	// 简单转换：将 core.Event 包装为 StreamEvent
	return StreamEvent{
		Type:     string(event.Type),
		TaskID:   event.ActionID,  // ActionID 映射为 TaskID
		Data:     event.Payload,
		Sequence: 0,
		Final:    false,
	}
}

// ConvertToEvent 将 StreamEvent 转换为 core.Event（保留兼容性）。
func (b *StreamEventBusImpl) ConvertToEvent(streamEvent StreamEvent) core.Event {
	payload, ok := streamEvent.Data.(map[string]interface{})
	if !ok {
		payload = map[string]interface{}{"data": streamEvent.Data}
	}
	return core.Event{
		ID:        "",  // 将由 NewEvent 生成
		Type:      core.EventType(streamEvent.Type),
		ActionID:  streamEvent.TaskID,
		Payload:   payload,
	}
}

// Close 关闭事件总线，清理所有通道。
func (b *StreamEventBusImpl) Close() error {
	b.channels.Range(func(key, value any) bool {
		ch := value.(chan StreamEvent)
		close(ch)
		return true
	})
	b.channels = sync.Map{}
	b.adapters = sync.Map{}
	return nil
}

// CloseTask 关闭指定任务的流式通道。
func (b *StreamEventBusImpl) CloseTask(taskID string) {
	if ch, ok := b.channels.LoadAndDelete(taskID); ok {
		close(ch.(chan StreamEvent))
	}
	b.adapters.Delete(taskID)
}

// GetAdapter 获取指定任务的适配器。
func (b *StreamEventBusImpl) GetAdapter(taskID string) (*StreamAdapter, bool) {
	adapter, ok := b.adapters.Load(taskID)
	if !ok {
		return nil, false
	}
	return adapter.(*StreamAdapter), true
}
