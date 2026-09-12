package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/rs/zerolog"
)

// Runner 是工作流执行器
type Runner struct {
	config  *WorkflowConfig
	agents  map[string]core.Agent
	factory AgentFactory
	logger  zerolog.Logger

	// 执行状态
	mu      sync.RWMutex
	context map[string]interface{} // 上下文变量
	outputs map[string]interface{} // 步骤输出
}

// AgentFactory 是 Agent 工厂接口
type AgentFactory interface {
	// CreateAgent 根据配置创建 Agent
	CreateAgent(config AgentConfig) (core.Agent, error)
}

// RunnerOption 是 Runner 配置选项
type RunnerOption func(*Runner)

// WithLogger 设置日志器
func WithLogger(logger zerolog.Logger) RunnerOption {
	return func(r *Runner) {
		r.logger = logger
	}
}

// WithContext 设置初始上下文
func WithContext(ctx map[string]interface{}) RunnerOption {
	return func(r *Runner) {
		r.context = ctx
	}
}

// NewRunner 创建工作流执行器
func NewRunner(config *WorkflowConfig, factory AgentFactory, opts ...RunnerOption) (*Runner, error) {
	if config == nil {
		return nil, fmt.Errorf("config is required")
	}

	if factory == nil {
		return nil, fmt.Errorf("agent factory is required")
	}

	r := &Runner{
		config:  config,
		agents:  make(map[string]core.Agent),
		factory: factory,
		logger:  zerolog.Nop(),
		context: make(map[string]interface{}),
		outputs: make(map[string]interface{}),
	}

	// 应用选项
	for _, opt := range opts {
		opt(r)
	}

	return r, nil
}

// Run 执行工作流
func (r *Runner) Run(ctx context.Context) error {
	r.logger.Info().
		Str("workflow", r.config.Name).
		Int("agents", len(r.config.Agents)).
		Int("steps", len(r.config.Workflow)).
		Msg("starting workflow execution")

	// 1. 初始化所有 Agents
	if err := r.initializeAgents(ctx); err != nil {
		return fmt.Errorf("initialize agents: %w", err)
	}

	// 2. 按顺序执行步骤
	for i, step := range r.config.Workflow {
		r.logger.Info().
			Int("step_index", i).
			Str("step_name", step.Name).
			Str("agent", step.Agent).
			Msg("executing step")

		if err := r.executeStep(ctx, step); err != nil {
			return fmt.Errorf("execute step %q: %w", step.Name, err)
		}
	}

	r.logger.Info().
		Str("workflow", r.config.Name).
		Msg("workflow execution completed")

	return nil
}

// initializeAgents 初始化所有 Agents
func (r *Runner) initializeAgents(ctx context.Context) error {
	for _, agentConfig := range r.config.Agents {
		r.logger.Debug().
			Str("agent_name", agentConfig.Name).
			Str("agent_type", agentConfig.Type).
			Msg("creating agent")

		agent, err := r.factory.CreateAgent(agentConfig)
		if err != nil {
			return fmt.Errorf("create agent %q: %w", agentConfig.Name, err)
		}

		r.agents[agentConfig.Name] = agent

		r.logger.Debug().
			Str("agent_name", agentConfig.Name).
			Msg("agent created successfully")
	}

	return nil
}

// executeStep 执行单个步骤
func (r *Runner) executeStep(ctx context.Context, step StepConfig) error {
	// 获取 Agent
	agent, ok := r.agents[step.Agent]
	if !ok {
		return fmt.Errorf("agent %q not found", step.Agent)
	}

	// 准备输入（PoC 版本：直接使用，不处理模板）
	input := step.Input

	// 设置超时
	timeout := time.Duration(step.Timeout) * time.Second
	if timeout == 0 {
		timeout = time.Duration(DefaultTimeout()) * time.Second
	}

	stepCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// 执行（PoC 版本：简化实现，不实际调用 Agent）
	r.logger.Debug().
		Str("step", step.Name).
		Str("agent", step.Agent).
		Interface("input", input).
		Msg("step input prepared")

	// PoC 版本：模拟执行
	output := map[string]interface{}{
		"status":  "success",
		"step":    step.Name,
		"agent":   step.Agent,
		"message": fmt.Sprintf("Step %q executed by agent %q", step.Name, step.Agent),
	}

	// 保存输出
	if step.Output != "" {
		r.mu.Lock()
		r.outputs[step.Name] = output
		r.mu.Unlock()

		r.logger.Debug().
			Str("step", step.Name).
			Str("output_key", step.Output).
			Msg("step output saved")
	}

	// 防止未使用变量警告
	_ = agent
	_ = stepCtx

	return nil
}

// GetOutput 获取步骤输出
func (r *Runner) GetOutput(stepName string) (interface{}, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	output, ok := r.outputs[stepName]
	return output, ok
}

// GetAllOutputs 获取所有步骤输出
func (r *Runner) GetAllOutputs() map[string]interface{} {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// 返回副本
	outputs := make(map[string]interface{}, len(r.outputs))
	for k, v := range r.outputs {
		outputs[k] = v
	}

	return outputs
}

// Cleanup 清理资源
func (r *Runner) Cleanup(ctx context.Context) error {
	r.logger.Info().Msg("cleaning up workflow resources")

	for name, agent := range r.agents {
		if err := agent.Stop(ctx); err != nil {
			r.logger.Warn().
				Err(err).
				Str("agent", name).
				Msg("failed to stop agent")
		}
	}

	return nil
}

// MockAgentFactory 是用于测试的 Mock Agent Factory
type MockAgentFactory struct {
	agents map[string]core.Agent
}

// NewMockAgentFactory 创建 Mock Agent Factory
func NewMockAgentFactory() *MockAgentFactory {
	return &MockAgentFactory{
		agents: make(map[string]core.Agent),
	}
}

// RegisterAgent 注册 Agent
func (f *MockAgentFactory) RegisterAgent(name string, agent core.Agent) {
	f.agents[name] = agent
}

// CreateAgent 实现 AgentFactory 接口
func (f *MockAgentFactory) CreateAgent(config AgentConfig) (core.Agent, error) {
	// 根据 type 返回预注册的 Agent
	agent, ok := f.agents[config.Type]
	if !ok {
		// 如果没有预注册，返回一个 Mock Agent
		return NewMockAgent(config.Name), nil
	}

	return agent, nil
}

// MockAgent 是用于测试的 Mock Agent
type MockAgent struct {
	name string
}

// NewMockAgent 创建 Mock Agent
func NewMockAgent(name string) *MockAgent {
	return &MockAgent{name: name}
}

// Name 实现 core.Agent 接口
func (m *MockAgent) Name() string {
	return m.name
}

// Run 实现 core.Agent 接口
func (m *MockAgent) Run(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

// Stop 实现 core.Agent 接口
func (m *MockAgent) Stop(ctx context.Context) error {
	return nil
}

// ExportState 实现 core.Recoverable 接口
func (m *MockAgent) ExportState() (json.RawMessage, error) {
	return json.RawMessage(`{"name":"` + m.name + `"}`), nil
}

// ImportState 实现 core.Recoverable 接口
func (m *MockAgent) ImportState(data json.RawMessage) error {
	return nil
}
