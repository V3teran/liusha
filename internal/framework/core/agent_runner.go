package core

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

// AgentRunner 管理多个 Agent 的生命周期。
//
// 核心职责：
// - 统一启动多个 Agent（并发）
// - 统一停止所有 Agent（优雅退出）
// - 统一错误处理和日志收集
// - 统一指标收集
//
// 设计原则：
// - Agent 是长期运行的进程（持续监听事件、定期执行任务）
// - Runner 不管理 Agent 之间的协作关系（由业务层定义）
// - Runner 只关注生命周期管理（启动、停止、监控）
type AgentRunner struct {
	agents []Agent
	logger zerolog.Logger

	// 运行状态
	mu      sync.RWMutex
	running bool
	stopCh  chan struct{}

	// 错误收集
	errors []AgentError

	// 启动/停止超时
	startTimeout time.Duration
	stopTimeout  time.Duration
}

// AgentError 记录 Agent 运行时错误
type AgentError struct {
	AgentName string
	Error     error
	Timestamp time.Time
}

// AgentRunnerConfig 是 AgentRunner 的配置
type AgentRunnerConfig struct {
	// 日志器
	Logger zerolog.Logger

	// 启动超时（默认 30 秒）
	StartTimeout time.Duration

	// 停止超时（默认 10 秒）
	StopTimeout time.Duration
}

// NewAgentRunner 创建 AgentRunner 实例
func NewAgentRunner(config AgentRunnerConfig) *AgentRunner {
	if config.StartTimeout == 0 {
		config.StartTimeout = 30 * time.Second
	}
	if config.StopTimeout == 0 {
		config.StopTimeout = 10 * time.Second
	}

	return &AgentRunner{
		agents:       make([]Agent, 0),
		logger:       config.Logger.With().Str("component", "agent_runner").Logger(),
		running:      false,
		stopCh:       make(chan struct{}),
		errors:       make([]AgentError, 0),
		startTimeout: config.StartTimeout,
		stopTimeout:  config.StopTimeout,
	}
}

// AddAgent 添加 Agent 到 Runner
//
// 必须在 Start 之前调用
func (r *AgentRunner) AddAgent(agent Agent) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.running {
		return fmt.Errorf("runner 已启动，无法添加 Agent")
	}

	if agent == nil {
		return fmt.Errorf("agent 不能为 nil")
	}

	// 检查重复
	for _, a := range r.agents {
		if a.Name() == agent.Name() {
			return fmt.Errorf("agent %s 已存在", agent.Name())
		}
	}

	r.agents = append(r.agents, agent)
	r.logger.Info().Str("agent", agent.Name()).Msg("agent 已添加")
	return nil
}

// Start 启动所有 Agent
//
// 阻塞式运行，直到所有 Agent 停止或 context 取消
func (r *AgentRunner) Start(ctx context.Context) error {
	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return fmt.Errorf("runner 已在运行")
	}
	if len(r.agents) == 0 {
		r.mu.Unlock()
		return fmt.Errorf("没有 Agent 可启动")
	}
	r.running = true
	r.stopCh = make(chan struct{})
	r.mu.Unlock()

	r.logger.Info().Int("agent_count", len(r.agents)).Msg("开始启动所有 Agent")

	// 启动所有 Agent（并发）
	var wg sync.WaitGroup
	errCh := make(chan AgentError, len(r.agents))

	for _, agent := range r.agents {
		wg.Add(1)
		go func(a Agent) {
			defer wg.Done()

			agentCtx, cancel := context.WithTimeout(ctx, r.startTimeout)
			defer cancel()

			r.logger.Info().Str("agent", a.Name()).Msg("启动 Agent")

			startTime := time.Now()
			if err := a.Run(agentCtx); err != nil && ctx.Err() == nil {
				// 只记录非取消错误
				errCh <- AgentError{
					AgentName: a.Name(),
					Error:     err,
					Timestamp: time.Now(),
				}
				r.logger.Error().
					Err(err).
					Str("agent", a.Name()).
					Dur("duration", time.Since(startTime)).
					Msg("Agent 异常停止")
			} else {
				r.logger.Info().
					Str("agent", a.Name()).
					Dur("duration", time.Since(startTime)).
					Msg("Agent 正常停止")
			}
		}(agent)
	}

	// 等待所有 Agent 启动完成
	go func() {
		wg.Wait()
		close(errCh)
		close(r.stopCh)
	}()

	// 收集错误
	for err := range errCh {
		r.mu.Lock()
		r.errors = append(r.errors, err)
		r.mu.Unlock()
	}

	// 等待所有 Agent 停止
	<-r.stopCh

	r.mu.Lock()
	r.running = false
	r.mu.Unlock()

	r.logger.Info().Msg("所有 Agent 已停止")

	// 如果有错误，返回第一个
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.errors) > 0 {
		return fmt.Errorf("有 %d 个 Agent 异常停止，第一个错误: %w",
			len(r.errors), r.errors[0].Error)
	}

	return nil
}

// Stop 停止所有 Agent
//
// 优雅退出：给每个 Agent 发送停止信号，等待它们完成当前任务
func (r *AgentRunner) Stop(ctx context.Context) error {
	r.mu.RLock()
	if !r.running {
		r.mu.RUnlock()
		return fmt.Errorf("runner 未运行")
	}
	agents := r.agents
	r.mu.RUnlock()

	r.logger.Info().Int("agent_count", len(agents)).Msg("开始停止所有 Agent")

	// 停止所有 Agent（并发）
	var wg sync.WaitGroup
	errCh := make(chan error, len(agents))

	stopCtx, cancel := context.WithTimeout(ctx, r.stopTimeout)
	defer cancel()

	for _, agent := range agents {
		wg.Add(1)
		go func(a Agent) {
			defer wg.Done()

			r.logger.Info().Str("agent", a.Name()).Msg("停止 Agent")

			if err := a.Stop(stopCtx); err != nil {
				errCh <- fmt.Errorf("停止 Agent %s 失败: %w", a.Name(), err)
				r.logger.Error().Err(err).Str("agent", a.Name()).Msg("停止 Agent 失败")
			} else {
				r.logger.Info().Str("agent", a.Name()).Msg("Agent 已停止")
			}
		}(agent)
	}

	// 等待所有停止完成
	wg.Wait()
	close(errCh)

	// 收集错误
	var stopErrors []error
	for err := range errCh {
		stopErrors = append(stopErrors, err)
	}

	if len(stopErrors) > 0 {
		return fmt.Errorf("有 %d 个 Agent 停止失败: %v", len(stopErrors), stopErrors)
	}

	r.logger.Info().Msg("所有 Agent 已优雅停止")
	return nil
}

// IsRunning 检查 Runner 是否在运行
func (r *AgentRunner) IsRunning() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.running
}

// GetErrors 获取所有 Agent 错误
func (r *AgentRunner) GetErrors() []AgentError {
	r.mu.RLock()
	defer r.mu.RUnlock()

	errors := make([]AgentError, len(r.errors))
	copy(errors, r.errors)
	return errors
}

// GetAgentCount 获取 Agent 数量
func (r *AgentRunner) GetAgentCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.agents)
}

// GetAgents 获取所有 Agent（只读）
func (r *AgentRunner) GetAgents() []Agent {
	r.mu.RLock()
	defer r.mu.RUnlock()

	agents := make([]Agent, len(r.agents))
	copy(agents, r.agents)
	return agents
}
