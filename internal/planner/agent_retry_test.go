package planner

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"

	"github.com/V3teran/liusha/internal/bus"
	"github.com/V3teran/liusha/internal/explorationgraph"
)

// fakePlanner 实现 Planner 接口用于测试
type fakePlanner struct {
	callCount int32
	errors    []error
	result    []explorationgraph.Node
}

func (f *fakePlanner) Plan(_ context.Context, _ *explorationgraph.Store, _ string, _ []string) ([]explorationgraph.Node, error) {
	idx := int(atomic.AddInt32(&f.callCount, 1)) - 1
	if idx < len(f.errors) {
		return nil, f.errors[idx]
	}
	return f.result, nil
}

func (f *fakePlanner) AnalyzeResults(_ context.Context, _ *explorationgraph.Store, _ string, _ []explorationgraph.Node) (*ResultAnalysis, error) {
	return &ResultAnalysis{}, nil
}

// fakeEventBus 简单的内存事件总线
type fakeEventBus struct{}

func (f *fakeEventBus) Publish(_ bus.Event) {}

func (f *fakeEventBus) SubscribeTask(_ string) *bus.TaskSubscription {
	return nil
}

func (f *fakeEventBus) PublishActionProposed(_, _ string) {}

func (f *fakeEventBus) PublishActionCompleted(_, _ string) {}

func (f *fakeEventBus) PublishAttemptGenerated(_, _ string, _ interface{}) {}

func (f *fakeEventBus) PublishVerificationPassed(_, _ string) {}

func (f *fakeEventBus) PublishVerificationRefuted(_, _ string) {}

func (f *fakeEventBus) PublishTaskConverged(_, _ string) {}

func (f *fakeEventBus) PublishReplanRequested(_, _ string) {}

func TestGenerateNewActions_RetryOn429(t *testing.T) {
	fake := &fakePlanner{
		errors: []error{
			errors.New("429 Too Many Requests"),
		},
		result: []explorationgraph.Node{{ID: "action-1"}},
	}

	agent := &Agent{
		taskID:        "test-task",
		agentRunID:    "test-run",
		graph:         nil,
		planner:       fake,
		eventBus:      &fakeEventBus{},
		logger:        zerolog.Nop(),
		functionTools: []string{},
	}

	ctx := context.Background()
	actions, err := agent.generateNewActions(ctx)

	assert.NoError(t, err)
	assert.Len(t, actions, 1)
	assert.Equal(t, "action-1", actions[0].ID)
	assert.Equal(t, int32(2), fake.callCount) // 第一次失败，第二次成功
}

func TestGenerateNewActions_RetryOnTimeout(t *testing.T) {
	fake := &fakePlanner{
		errors: []error{
			errors.New("context deadline exceeded"),
			errors.New("context deadline exceeded"),
		},
		result: []explorationgraph.Node{{ID: "action-1"}},
	}

	agent := &Agent{
		taskID:        "test-task",
		agentRunID:    "test-run",
		graph:         nil,
		planner:       fake,
		eventBus:      &fakeEventBus{},
		logger:        zerolog.Nop(),
		functionTools: []string{},
	}

	ctx := context.Background()
	actions, err := agent.generateNewActions(ctx)

	assert.NoError(t, err)
	assert.Len(t, actions, 1)
	assert.Equal(t, int32(3), fake.callCount) // 两次失败，第三次成功
}

func TestGenerateNewActions_ExhaustRetries(t *testing.T) {
	fake := &fakePlanner{
		errors: []error{
			errors.New("429 Too Many Requests"),
			errors.New("429 Too Many Requests"),
			errors.New("429 Too Many Requests"),
			errors.New("429 Too Many Requests"),
		},
	}

	agent := &Agent{
		taskID:        "test-task",
		agentRunID:    "test-run",
		graph:         nil,
		planner:       fake,
		eventBus:      &fakeEventBus{},
		logger:        zerolog.Nop(),
		functionTools: []string{},
	}

	ctx := context.Background()
	actions, err := agent.generateNewActions(ctx)

	assert.Error(t, err)
	assert.Nil(t, actions)
	assert.Contains(t, err.Error(), "重试 4 次后仍失败")
	assert.Equal(t, int32(4), fake.callCount) // 总共 4 次调用
}

func TestGenerateNewActions_NonRetriableError(t *testing.T) {
	fake := &fakePlanner{
		errors: []error{
			errors.New("invalid JSON response"),
		},
	}

	agent := &Agent{
		taskID:        "test-task",
		agentRunID:    "test-run",
		graph:         nil,
		planner:       fake,
		eventBus:      &fakeEventBus{},
		logger:        zerolog.Nop(),
		functionTools: []string{},
	}

	ctx := context.Background()
	actions, err := agent.generateNewActions(ctx)

	assert.Error(t, err)
	assert.Nil(t, actions)
	assert.Contains(t, err.Error(), "invalid JSON response")
	assert.Equal(t, int32(1), fake.callCount) // 不可重试，只调用 1 次
}

func TestIsRetriableError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"nil error", nil, false},
		{"429 error", errors.New("429 Too Many Requests"), true},
		{"timeout error", errors.New("context deadline exceeded"), true},
		{"connection refused", errors.New("connection refused"), true},
		{"connection reset", errors.New("connection reset by peer"), true},
		{"EOF", errors.New("unexpected EOF"), true},
		{"non-retriable", errors.New("invalid JSON"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isRetriableError(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGenerateNewActions_RespectContextCancellation(t *testing.T) {
	fake := &fakePlanner{
		errors: []error{
			errors.New("429 Too Many Requests"),
		},
	}

	agent := &Agent{
		taskID:        "test-task",
		agentRunID:    "test-run",
		graph:         nil,
		planner:       fake,
		eventBus:      &fakeEventBus{},
		logger:        zerolog.Nop(),
		functionTools: []string{},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	actions, err := agent.generateNewActions(ctx)

	assert.Error(t, err)
	assert.Nil(t, actions)
	// context 取消后应该立即返回，不会重试
	assert.Equal(t, int32(1), fake.callCount)
}
