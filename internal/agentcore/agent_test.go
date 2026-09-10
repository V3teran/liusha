package agentcore

import (
	"context"
	"testing"

	"github.com/V3teran/liusha/internal/provider"
	"github.com/V3teran/liusha/internal/registry"
	"github.com/rs/zerolog"
)

// TestNew 测试 Agent 创建
func TestNew(t *testing.T) {
	logger := zerolog.Nop()
	reg := registry.New()

	tests := []struct {
		name     string
		cfg      Config
		wantName string
		wantMax  int
	}{
		{
			name: "default values",
			cfg: Config{
				Name:     "test-agent",
				Registry: reg,
				Logger:   logger,
			},
			wantName: "test-agent",
			wantMax:  100,
		},
		{
			name: "custom values",
			cfg: Config{
				Name:      "custom-agent",
				Registry:  reg,
				Logger:    logger,
				MaxRounds: 50,
				MaxTokens: 4000,
			},
			wantName: "custom-agent",
			wantMax:  50,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent := New(tt.cfg)
			if agent.name != tt.wantName {
				t.Errorf("name = %v, want %v", agent.name, tt.wantName)
			}
			if agent.maxRounds != tt.wantMax {
				t.Errorf("maxRounds = %v, want %v", agent.maxRounds, tt.wantMax)
			}
		})
	}
}

// MockProvider 是测试用的 Provider
type MockProvider struct {
	responses []provider.Response
	callCount int
}

func (m *MockProvider) Complete(ctx context.Context, req provider.Request) (provider.Response, error) {
	if m.callCount >= len(m.responses) {
		return provider.Response{}, nil
	}
	resp := m.responses[m.callCount]
	m.callCount++
	return resp, nil
}

func (m *MockProvider) CountTokens(ctx context.Context, req provider.Request) (int, error) {
	return 100, nil
}

// TestRunToolLoop_NoToolCalls 测试无工具调用场景
func TestRunToolLoop_NoToolCalls(t *testing.T) {
	logger := zerolog.Nop()
	reg := registry.New()

	mockProvider := &MockProvider{
		responses: []provider.Response{
			{
				Content:   "完成任务",
				ToolCalls: nil,
			},
		},
	}

	agent := New(Config{
		Name:         "test-agent",
		Provider:     mockProvider,
		Registry:     reg,
		Logger:       logger,
		SystemPrompt: "你是测试 Agent",
	})

	ctx := context.Background()
	resp, err := agent.RunToolLoop(ctx, "测试任务")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Rounds != 1 {
		t.Errorf("rounds = %d, want 1", resp.Rounds)
	}
	if resp.Halt != HaltDone {
		t.Errorf("halt = %v, want %v", resp.Halt, HaltDone)
	}
	if resp.Content != "完成任务" {
		t.Errorf("content = %v, want '完成任务'", resp.Content)
	}
}

// TestRunToolLoop_MaxRounds 测试达到最大轮数
func TestRunToolLoop_MaxRounds(t *testing.T) {
	logger := zerolog.Nop()
	reg := registry.New()

	// 模拟一直返回工具调用
	mockProvider := &MockProvider{
		responses: []provider.Response{
			{ToolCalls: []provider.ToolCall{{ID: "1", Name: "test"}}},
			{ToolCalls: []provider.ToolCall{{ID: "2", Name: "test"}}},
			{ToolCalls: []provider.ToolCall{{ID: "3", Name: "test"}}},
		},
	}

	agent := New(Config{
		Name:         "test-agent",
		Provider:     mockProvider,
		Registry:     reg,
		Logger:       logger,
		MaxRounds:    3,
		SystemPrompt: "你是测试 Agent",
	})

	ctx := context.Background()
	resp, err := agent.RunToolLoop(ctx, "测试任务")

	if err == nil {
		t.Fatal("expected error for max rounds")
	}
	if resp.Rounds != 3 {
		t.Errorf("rounds = %d, want 3", resp.Rounds)
	}
	if resp.Halt != HaltMaxRounds {
		t.Errorf("halt = %v, want %v", resp.Halt, HaltMaxRounds)
	}
}
