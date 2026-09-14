package core

import (
	"context"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/framework/llm"
)

func TestCallbackChain(t *testing.T) {
	// 创建两个 Callback
	metrics := NewMetricsCallback()

	// 创建链
	chain := NewCallbackChain(metrics)

	ctx := context.Background()

	// 测试 Agent 回调
	t.Run("agent callbacks", func(t *testing.T) {
		startEvent := AgentStartEvent{
			AgentName: "test-agent",
			TaskID:    "task-1",
			StartTime: time.Now(),
		}
		chain.OnAgentStart(ctx, startEvent)

		endEvent := AgentEndEvent{
			AgentName: "test-agent",
			TaskID:    "task-1",
			StartTime: startEvent.StartTime,
			EndTime:   time.Now(),
			Duration:  100 * time.Millisecond,
			Error:     nil,
		}
		chain.OnAgentEnd(ctx, endEvent)

		// 验证指标
		m := metrics.GetMetrics()
		if m.AgentStartCount != 1 {
			t.Errorf("expected 1 agent start, got %d", m.AgentStartCount)
		}
		if m.AgentEndCount != 1 {
			t.Errorf("expected 1 agent end, got %d", m.AgentEndCount)
		}
	})

	// 测试 Tool 回调
	t.Run("tool callbacks", func(t *testing.T) {
		startEvent := ToolStartEvent{
			ToolName:  "test-tool",
			TaskID:    "task-1",
			Input:     map[string]any{"param": "value"},
			StartTime: time.Now(),
		}
		chain.OnToolStart(ctx, startEvent)

		endEvent := ToolEndEvent{
			ToolName:  "test-tool",
			TaskID:    "task-1",
			Input:     startEvent.Input,
			Output:    map[string]any{"result": "ok"},
			StartTime: startEvent.StartTime,
			EndTime:   time.Now(),
			Duration:  50 * time.Millisecond,
			Error:     nil,
		}
		chain.OnToolEnd(ctx, endEvent)

		// 验证指标
		m := metrics.GetMetrics()
		if m.ToolStartCount != 1 {
			t.Errorf("expected 1 tool start, got %d", m.ToolStartCount)
		}
		if m.ToolEndCount != 1 {
			t.Errorf("expected 1 tool end, got %d", m.ToolEndCount)
		}
	})

	// 测试 LLM 回调
	t.Run("llm callbacks", func(t *testing.T) {
		startEvent := LLMStartEvent{
			ProviderID: "anthropic",
			ModelID:    "claude-sonnet-4-6",
			TaskID:     "task-1",
			Request: llm.Request{
				Messages:  []llm.Message{{Role: llm.RoleUser, Content: "test"}},
				MaxTokens: 1000,
			},
			StartTime: time.Now(),
		}
		chain.OnLLMStart(ctx, startEvent)

		endEvent := LLMEndEvent{
			ProviderID: "anthropic",
			ModelID:    "claude-sonnet-4-6",
			TaskID:     "task-1",
			Request:    startEvent.Request,
			Response: llm.Response{
				Content: "response",
				Usage: llm.Usage{
					InTokens:  10,
					OutTokens: 20,
				},
				FinishReason: "stop",
			},
			StartTime: startEvent.StartTime,
			EndTime:   time.Now(),
			Duration:  200 * time.Millisecond,
			Error:     nil,
		}
		chain.OnLLMEnd(ctx, endEvent)

		// 验证指标
		m := metrics.GetMetrics()
		if m.LLMStartCount != 1 {
			t.Errorf("expected 1 llm start, got %d", m.LLMStartCount)
		}
		if m.LLMEndCount != 1 {
			t.Errorf("expected 1 llm end, got %d", m.LLMEndCount)
		}
		if m.LLMTotalInTokens != 10 {
			t.Errorf("expected 10 in tokens, got %d", m.LLMTotalInTokens)
		}
		if m.LLMTotalOutTokens != 20 {
			t.Errorf("expected 20 out tokens, got %d", m.LLMTotalOutTokens)
		}
	})
}

func TestMetricsCallback(t *testing.T) {
	metrics := NewMetricsCallback()
	ctx := context.Background()

	// 模拟多次调用
	for i := 0; i < 5; i++ {
		startTime := time.Now()

		llmStart := LLMStartEvent{
			ProviderID: "anthropic",
			ModelID:    "claude-sonnet-4-6",
			TaskID:     "task-1",
			StartTime:  startTime,
		}
		metrics.OnLLMStart(ctx, llmStart)

		llmEnd := LLMEndEvent{
			ProviderID: "anthropic",
			ModelID:    "claude-sonnet-4-6",
			TaskID:     "task-1",
			Response: llm.Response{
				Usage: llm.Usage{
					InTokens:  100,
					OutTokens: 200,
				},
			},
			StartTime: startTime,
			EndTime:   time.Now(),
			Duration:  100 * time.Millisecond,
		}
		metrics.OnLLMEnd(ctx, llmEnd)
	}

	// 验证统计
	m := metrics.GetMetrics()
	if m.LLMStartCount != 5 {
		t.Errorf("expected 5 llm starts, got %d", m.LLMStartCount)
	}
	if m.LLMTotalInTokens != 500 {
		t.Errorf("expected 500 total in tokens, got %d", m.LLMTotalInTokens)
	}
	if m.LLMTotalOutTokens != 1000 {
		t.Errorf("expected 1000 total out tokens, got %d", m.LLMTotalOutTokens)
	}

	// 测试重置
	metrics.Reset()
	m = metrics.GetMetrics()
	if m.LLMStartCount != 0 {
		t.Errorf("expected 0 after reset, got %d", m.LLMStartCount)
	}
}

func TestErrorRate(t *testing.T) {
	metrics := NewMetricsCallback()
	ctx := context.Background()

	// 模拟 10 次调用，3 次失败
	for i := 0; i < 10; i++ {
		startTime := time.Now()

		toolStart := ToolStartEvent{
			ToolName:  "test-tool",
			TaskID:    "task-1",
			StartTime: startTime,
		}
		metrics.OnToolStart(ctx, toolStart)

		var err error
		if i < 3 {
			err = context.DeadlineExceeded
		}

		toolEnd := ToolEndEvent{
			ToolName:  "test-tool",
			TaskID:    "task-1",
			StartTime: startTime,
			EndTime:   time.Now(),
			Duration:  50 * time.Millisecond,
			Error:     err,
		}
		metrics.OnToolEnd(ctx, toolEnd)
	}

	// 验证错误率
	m := metrics.GetMetrics()
	if m.ToolEndCount != 10 {
		t.Errorf("expected 10 tool ends, got %d", m.ToolEndCount)
	}
	if m.ToolErrorCount != 3 {
		t.Errorf("expected 3 tool errors, got %d", m.ToolErrorCount)
	}

	expectedRate := 0.3
	if m.ToolErrorRate < expectedRate-0.01 || m.ToolErrorRate > expectedRate+0.01 {
		t.Errorf("expected error rate ~%.2f, got %.2f", expectedRate, m.ToolErrorRate)
	}
}
