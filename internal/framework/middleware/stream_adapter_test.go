package middleware

import (
	"context"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/framework/llm"
)

func TestStreamAdapter(t *testing.T) {
	adapter := NewStreamAdapter("task-123")

	t.Run("convert llm text stream", func(t *testing.T) {
		llmEvent := llm.StreamEvent{
			Kind:    llm.StreamText,
			Content: "Hello, world!",
		}

		streamEvent := adapter.ConvertLLMStream(llmEvent)

		if streamEvent.Type != "llm.text" {
			t.Errorf("expected type 'llm.text', got '%s'", streamEvent.Type)
		}
		if streamEvent.TaskID != "task-123" {
			t.Errorf("expected task_id 'task-123', got '%s'", streamEvent.TaskID)
		}
		if streamEvent.Sequence != 1 {
			t.Errorf("expected sequence 1, got %d", streamEvent.Sequence)
		}
		if streamEvent.Final {
			t.Error("expected Final=false for text event")
		}

		// 验证数据
		data, ok := streamEvent.Data.(LLMStreamEvent)
		if !ok {
			t.Fatal("expected LLMStreamEvent data")
		}
		if data.Delta != "Hello, world!" {
			t.Errorf("expected delta 'Hello, world!', got '%s'", data.Delta)
		}
	})

	t.Run("convert llm thinking stream", func(t *testing.T) {
		llmEvent := llm.StreamEvent{
			Kind:    llm.StreamThinking,
			Content: "Let me think...",
		}

		streamEvent := adapter.ConvertLLMStream(llmEvent)

		if streamEvent.Type != "llm.thinking" {
			t.Errorf("expected type 'llm.thinking', got '%s'", streamEvent.Type)
		}
		if streamEvent.Sequence != 2 { // 序列号递增
			t.Errorf("expected sequence 2, got %d", streamEvent.Sequence)
		}
	})

	t.Run("convert llm tool call stream", func(t *testing.T) {
		llmEvent := llm.StreamEvent{
			Kind: llm.StreamToolCall,
			Tool: &llm.ToolCall{
				ID:        "call-1",
				Name:      "search",
				Arguments: []byte(`{"query":"test"}`),
			},
		}

		streamEvent := adapter.ConvertLLMStream(llmEvent)

		if streamEvent.Type != "llm.tool_call" {
			t.Errorf("expected type 'llm.tool_call', got '%s'", streamEvent.Type)
		}

		data, ok := streamEvent.Data.(LLMStreamEvent)
		if !ok {
			t.Fatal("expected LLMStreamEvent data")
		}
		if len(data.ToolCalls) != 1 {
			t.Fatalf("expected 1 tool call, got %d", len(data.ToolCalls))
		}
		if data.ToolCalls[0].Name != "search" {
			t.Errorf("expected tool name 'search', got '%s'", data.ToolCalls[0].Name)
		}
	})

	t.Run("convert llm done stream", func(t *testing.T) {
		llmEvent := llm.StreamEvent{
			Kind: llm.StreamDone,
			Usage: &llm.Usage{
				InTokens:  100,
				OutTokens: 200,
			},
		}

		streamEvent := adapter.ConvertLLMStream(llmEvent)

		if streamEvent.Type != "llm.done" {
			t.Errorf("expected type 'llm.done', got '%s'", streamEvent.Type)
		}
		if !streamEvent.Final {
			t.Error("expected Final=true for done event")
		}
	})

	t.Run("convert llm error stream", func(t *testing.T) {
		llmEvent := llm.StreamEvent{
			Kind: llm.StreamError,
			Err:  context.DeadlineExceeded,
		}

		streamEvent := adapter.ConvertLLMStream(llmEvent)

		if streamEvent.Type != "llm.error" {
			t.Errorf("expected type 'llm.error', got '%s'", streamEvent.Type)
		}
		if !streamEvent.Final {
			t.Error("expected Final=true for error event")
		}
	})
}

func TestStreamEventBus(t *testing.T) {
	bus := NewStreamEventBus(nil) // EventBus 可以为 nil，独立测试
	defer bus.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	taskID := "task-456"

	t.Run("create and send events", func(t *testing.T) {
		// 创建流
		ch, err := bus.Stream(ctx, taskID)
		if err != nil {
			t.Fatalf("failed to create stream: %v", err)
		}

		// 发送事件
		event := StreamEvent{
			Type:     "test",
			TaskID:   taskID,
			Data:     map[string]any{"message": "hello"},
			Sequence: 1,
			Final:    false,
		}

		go func() {
			time.Sleep(50 * time.Millisecond)
			if err := bus.Send(ctx, event); err != nil {
				t.Errorf("failed to send event: %v", err)
			}
		}()

		// 接收事件
		select {
		case received := <-ch:
			if received.Type != event.Type {
				t.Errorf("expected type '%s', got '%s'", event.Type, received.Type)
			}
			if received.TaskID != event.TaskID {
				t.Errorf("expected task_id '%s', got '%s'", event.TaskID, received.TaskID)
			}
		case <-ctx.Done():
			t.Fatal("timeout waiting for event")
		}
	})

	t.Run("close task stream", func(t *testing.T) {
		taskID2 := "task-789"
		ch, err := bus.Stream(ctx, taskID2)
		if err != nil {
			t.Fatalf("failed to create stream: %v", err)
		}

		// 关闭流
		bus.CloseTask(taskID2)

		// 验证通道已关闭
		select {
		case _, ok := <-ch:
			if ok {
				t.Error("expected channel to be closed")
			}
		case <-time.After(100 * time.Millisecond):
			t.Fatal("timeout waiting for channel close")
		}
	})

	t.Run("get adapter", func(t *testing.T) {
		taskID3 := "task-101"
		bus.Stream(ctx, taskID3)

		adapter, ok := bus.GetAdapter(taskID3)
		if !ok {
			t.Fatal("expected adapter to exist")
		}
		if adapter.taskID != taskID3 {
			t.Errorf("expected adapter task_id '%s', got '%s'", taskID3, adapter.taskID)
		}
	})
}

func TestProgressEventConversion(t *testing.T) {
	adapter := NewStreamAdapter("task-999")

	progress := ProgressEvent{
		Phase:          "execution",
		Progress:       50,
		Step:           "processing data",
		CompletedNodes: 5,
		TotalNodes:     10,
	}

	streamEvent := adapter.ConvertProgressEvent(progress)

	if streamEvent.Type != "progress" {
		t.Errorf("expected type 'progress', got '%s'", streamEvent.Type)
	}
	if streamEvent.TaskID != "task-999" {
		t.Errorf("expected task_id 'task-999', got '%s'", streamEvent.TaskID)
	}

	data, ok := streamEvent.Data.(ProgressEvent)
	if !ok {
		t.Fatal("expected ProgressEvent data")
	}
	if data.Progress != 50 {
		t.Errorf("expected progress 50, got %d", data.Progress)
	}
}

func TestLogEventConversion(t *testing.T) {
	adapter := NewStreamAdapter("task-888")

	logEvent := LogEvent{
		Level:     "info",
		Message:   "operation completed",
		Source:    "executor",
		Timestamp: time.Now().UnixMilli(),
	}

	streamEvent := adapter.ConvertLogEvent(logEvent)

	if streamEvent.Type != "log" {
		t.Errorf("expected type 'log', got '%s'", streamEvent.Type)
	}

	data, ok := streamEvent.Data.(LogEvent)
	if !ok {
		t.Fatal("expected LogEvent data")
	}
	if data.Level != "info" {
		t.Errorf("expected level 'info', got '%s'", data.Level)
	}
}
