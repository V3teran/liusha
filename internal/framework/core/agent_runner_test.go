package core

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MockAgent 是用于测试的 Mock Agent
type MockAgent struct {
	name     string
	runFunc  func(ctx context.Context) error
	stopFunc func(ctx context.Context) error
	runCh    chan struct{} // 用于同步测试
}

func NewMockAgent(name string) *MockAgent {
	return &MockAgent{
		name:  name,
		runCh: make(chan struct{}),
	}
}

func (m *MockAgent) Name() string {
	return m.name
}

func (m *MockAgent) Run(ctx context.Context) error {
	if m.runFunc != nil {
		return m.runFunc(ctx)
	}
	// 默认行为：等待 context 取消
	<-ctx.Done()
	close(m.runCh)
	return ctx.Err()
}

func (m *MockAgent) Stop(ctx context.Context) error {
	if m.stopFunc != nil {
		return m.stopFunc(ctx)
	}
	return nil
}

// ExportState 实现 Recoverable 接口
func (m *MockAgent) ExportState() (json.RawMessage, error) {
	return json.RawMessage("{}"), nil
}

// ImportState 实现 Recoverable 接口
func (m *MockAgent) ImportState(data json.RawMessage) error {
	return nil
}

// TestAgentRunner_Basic 测试基本功能
func TestAgentRunner_Basic(t *testing.T) {
	logger := zerolog.Nop()
	runner := NewAgentRunner(AgentRunnerConfig{
		Logger: logger,
	})

	// 添加 Agent
	agent1 := NewMockAgent("agent1")
	agent2 := NewMockAgent("agent2")

	require.NoError(t, runner.AddAgent(agent1))
	require.NoError(t, runner.AddAgent(agent2))

	assert.Equal(t, 2, runner.GetAgentCount())
	assert.False(t, runner.IsRunning())
}

// TestAgentRunner_DuplicateAgent 测试重复添加 Agent
func TestAgentRunner_DuplicateAgent(t *testing.T) {
	logger := zerolog.Nop()
	runner := NewAgentRunner(AgentRunnerConfig{
		Logger: logger,
	})

	agent := NewMockAgent("agent1")
	require.NoError(t, runner.AddAgent(agent))

	// 重复添加
	err := runner.AddAgent(agent)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "已存在")
}

// TestAgentRunner_StartStop 测试启动和停止
func TestAgentRunner_StartStop(t *testing.T) {
	logger := zerolog.Nop()
	runner := NewAgentRunner(AgentRunnerConfig{
		Logger:       logger,
		StartTimeout: 5 * time.Second,
		StopTimeout:  2 * time.Second,
	})

	// 添加 Agent
	agent1 := NewMockAgent("agent1")
	agent2 := NewMockAgent("agent2")

	require.NoError(t, runner.AddAgent(agent1))
	require.NoError(t, runner.AddAgent(agent2))

	// 启动 Runner（后台）
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- runner.Start(ctx)
	}()

	// 等待启动
	time.Sleep(100 * time.Millisecond)
	assert.True(t, runner.IsRunning())

	// 停止
	cancel()

	// 等待所有 Agent 停止
	select {
	case err := <-errCh:
		// context 取消错误是正常的
		if err != nil {
			assert.Contains(t, err.Error(), "context canceled")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("超时：Runner 未停止")
	}

	assert.False(t, runner.IsRunning())
}

// TestAgentRunner_AgentError 测试 Agent 运行时错误
func TestAgentRunner_AgentError(t *testing.T) {
	logger := zerolog.Nop()
	runner := NewAgentRunner(AgentRunnerConfig{
		Logger: logger,
	})

	// 添加会出错的 Agent
	agent := NewMockAgent("failing-agent")
	agent.runFunc = func(ctx context.Context) error {
		return assert.AnError
	}

	require.NoError(t, runner.AddAgent(agent))

	// 启动 Runner
	ctx := context.Background()
	err := runner.Start(ctx)

	// 应该返回错误
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "异常停止")

	// 检查错误记录
	errors := runner.GetErrors()
	assert.Len(t, errors, 1)
	assert.Equal(t, "failing-agent", errors[0].AgentName)
}

// TestAgentRunner_NoAgents 测试没有 Agent 时启动
func TestAgentRunner_NoAgents(t *testing.T) {
	logger := zerolog.Nop()
	runner := NewAgentRunner(AgentRunnerConfig{
		Logger: logger,
	})

	// 没有添加任何 Agent
	ctx := context.Background()
	err := runner.Start(ctx)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "没有 Agent")
}

// TestAgentRunner_AddAfterStart 测试启动后添加 Agent
func TestAgentRunner_AddAfterStart(t *testing.T) {
	logger := zerolog.Nop()
	runner := NewAgentRunner(AgentRunnerConfig{
		Logger: logger,
	})

	agent1 := NewMockAgent("agent1")
	require.NoError(t, runner.AddAgent(agent1))

	// 启动 Runner（后台）
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = runner.Start(ctx)
	}()

	time.Sleep(100 * time.Millisecond)

	// 尝试添加 Agent
	agent2 := NewMockAgent("agent2")
	err := runner.AddAgent(agent2)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "已启动")

	cancel()
}

// TestAgentRunner_ConcurrentAgents 测试并发运行多个 Agent
func TestAgentRunner_ConcurrentAgents(t *testing.T) {
	logger := zerolog.Nop()
	runner := NewAgentRunner(AgentRunnerConfig{
		Logger: logger,
	})

	// 添加 10 个 Agent
	for i := 0; i < 10; i++ {
		agent := NewMockAgent(string(rune('a' + i)))
		require.NoError(t, runner.AddAgent(agent))
	}

	assert.Equal(t, 10, runner.GetAgentCount())

	// 启动 Runner
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	err := runner.Start(ctx)

	// context 超时是预期的
	if err != nil {
		assert.Contains(t, err.Error(), "context")
	}
}

// TestAgentRunner_GracefulStop 测试优雅停止
func TestAgentRunner_GracefulStop(t *testing.T) {
	logger := zerolog.Nop()
	runner := NewAgentRunner(AgentRunnerConfig{
		Logger:      logger,
		StopTimeout: 2 * time.Second,
	})

	// 添加 Agent，模拟需要时间停止
	agent := NewMockAgent("slow-agent")
	stopCalled := false
	agent.stopFunc = func(ctx context.Context) error {
		stopCalled = true
		time.Sleep(500 * time.Millisecond) // 模拟清理工作
		return nil
	}

	require.NoError(t, runner.AddAgent(agent))

	// 启动 Runner（后台）
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = runner.Start(ctx)
	}()

	time.Sleep(100 * time.Millisecond)

	// 调用 Stop
	stopCtx := context.Background()
	err := runner.Stop(stopCtx)

	assert.NoError(t, err)
	assert.True(t, stopCalled)

	cancel()
}

// TestAgentRunner_StopTimeout 测试停止超时
func TestAgentRunner_StopTimeout(t *testing.T) {
	logger := zerolog.Nop()
	runner := NewAgentRunner(AgentRunnerConfig{
		Logger:      logger,
		StopTimeout: 500 * time.Millisecond,
	})

	// 添加永不停止的 Agent
	agent := NewMockAgent("hanging-agent")
	agent.stopFunc = func(ctx context.Context) error {
		<-ctx.Done() // 等待 context 取消
		return ctx.Err()
	}

	require.NoError(t, runner.AddAgent(agent))

	// 启动 Runner（后台）
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = runner.Start(ctx)
	}()

	time.Sleep(100 * time.Millisecond)

	// 调用 Stop（应该超时）
	stopCtx := context.Background()
	err := runner.Stop(stopCtx)

	// 应该返回超时错误
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "停止失败")

	cancel()
}

// TestAgentRunner_GetAgents 测试获取 Agent 列表
func TestAgentRunner_GetAgents(t *testing.T) {
	logger := zerolog.Nop()
	runner := NewAgentRunner(AgentRunnerConfig{
		Logger: logger,
	})

	agent1 := NewMockAgent("agent1")
	agent2 := NewMockAgent("agent2")

	require.NoError(t, runner.AddAgent(agent1))
	require.NoError(t, runner.AddAgent(agent2))

	agents := runner.GetAgents()
	assert.Len(t, agents, 2)
	assert.Equal(t, "agent1", agents[0].Name())
	assert.Equal(t, "agent2", agents[1].Name())
}
