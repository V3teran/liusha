package sandbox

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
)

// SingletonManager 是单例 Sandbox 管理器。
// 在 Runner 启动时创建一个常驻容器，所有任务共享此容器。
// 适用场景：单用户、开发测试环境、低并发（<5 任务）。
type SingletonManager struct {
	launcher Launcher
	logger   zerolog.Logger

	mu      sync.RWMutex
	sandbox *Sandbox // 单例容器

	// 监控指标
	acquireTotal   atomic.Int64
	acquireErrors  atomic.Int64
	acquireLatency atomic.Int64 // 累计延迟（微秒）

	// 配置
	autoRestart       bool          // 容器崩溃后自动重启
	healthCheckPeriod time.Duration // 健康检查间隔
	shutdownTimeout   time.Duration // 关闭超时

	// 生命周期
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// SingletonConfig 是 SingletonManager 的配置。
type SingletonConfig struct {
	AutoRestart       bool          // 容器崩溃后自动重启（默认 true）
	HealthCheckPeriod time.Duration // 健康检查间隔（默认 30s）
	ShutdownTimeout   time.Duration // 关闭超时（默认 30s）
}

// NewSingletonManager 创建单例管理器。
func NewSingletonManager(launcher Launcher, logger zerolog.Logger, cfg SingletonConfig) *SingletonManager {
	// 默认值
	if cfg.HealthCheckPeriod == 0 {
		cfg.HealthCheckPeriod = 30 * time.Second
	}
	if cfg.ShutdownTimeout == 0 {
		cfg.ShutdownTimeout = 30 * time.Second
	}

	ctx, cancel := context.WithCancel(context.Background())

	m := &SingletonManager{
		launcher:          launcher,
		logger:            logger,
		autoRestart:       cfg.AutoRestart,
		healthCheckPeriod: cfg.HealthCheckPeriod,
		shutdownTimeout:   cfg.ShutdownTimeout,
		ctx:               ctx,
		cancel:            cancel,
	}

	return m
}

// EnsureRunning 确保 Sandbox 容器正在运行。
// 如果容器不存在或不健康，则创建/重启。
// 此方法应在 Runner 启动时调用一次。
func (m *SingletonManager) EnsureRunning(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 如果已有容器，检查健康
	if m.sandbox != nil {
		if err := m.checkHealth(ctx, m.sandbox); err == nil {
			m.logger.Info().
				Str("container_id", m.sandbox.ID).
				Msg("singleton sandbox already running and healthy")
			return nil
		}

		// 不健康，销毁旧容器
		m.logger.Warn().
			Str("container_id", m.sandbox.ID).
			Msg("singleton sandbox unhealthy, recreating")
		_ = m.launcher.Destroy(ctx, m.sandbox.ID)
		m.sandbox = nil
	}

	// 创建新容器
	client, err := m.launcher.Spawn(ctx, "system-sandbox")
	if err != nil {
		m.logger.Error().Err(err).Msg("failed to spawn singleton sandbox")
		return fmt.Errorf("spawn singleton sandbox: %w", err)
	}

	m.sandbox = &Sandbox{
		Client:    client,
		ID:        "system-sandbox",
		CreatedAt: time.Now(),
	}

	m.logger.Info().
		Str("container_id", m.sandbox.ID).
		Msg("singleton sandbox created successfully")

	// 启动后台健康检查
	if m.autoRestart {
		m.wg.Add(1)
		go m.healthCheckLoop()
	}

	return nil
}

// Acquire 获取 Sandbox（单例模式直接返回）。
func (m *SingletonManager) Acquire(ctx context.Context, req AcquireRequest) (*Sandbox, error) {
	start := time.Now()
	defer func() {
		m.acquireTotal.Add(1)
		m.acquireLatency.Add(time.Since(start).Microseconds())
	}()

	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.sandbox == nil {
		m.acquireErrors.Add(1)
		return nil, fmt.Errorf("singleton sandbox not initialized (call EnsureRunning first)")
	}

	// 准备任务工作目录
	sb := &Sandbox{
		Client:    m.sandbox.Client,
		ID:        m.sandbox.ID,
		TaskID:    req.TaskID,
		WorkDir:   req.WorkDir,
		CreatedAt: m.sandbox.CreatedAt,
	}

	if sb.WorkDir == "" {
		sb.WorkDir = "/liusha/" + req.TaskID
	}

	if err := sb.PrepareWorkDir(ctx); err != nil {
		m.acquireErrors.Add(1)
		m.logger.Error().
			Err(err).
			Str("task_id", req.TaskID).
			Str("work_dir", sb.WorkDir).
			Msg("failed to prepare work dir")
		return nil, fmt.Errorf("prepare work dir: %w", err)
	}

	m.logger.Debug().
		Str("task_id", req.TaskID).
		Str("work_dir", sb.WorkDir).
		Msg("singleton sandbox acquired")

	return sb, nil
}

// Release 释放 Sandbox（单例模式只清理工作目录，不销毁容器）。
func (m *SingletonManager) Release(ctx context.Context, sb *Sandbox) error {
	if sb == nil {
		return nil
	}

	// 清理任务工作目录
	if err := sb.CleanupWorkDir(ctx); err != nil {
		m.logger.Warn().
			Err(err).
			Str("task_id", sb.TaskID).
			Str("work_dir", sb.WorkDir).
			Msg("failed to cleanup work dir")
		// 不返回错误，允许继续
	}

	m.logger.Debug().
		Str("task_id", sb.TaskID).
		Msg("singleton sandbox released")

	return nil
}

// Healthz 健康检查。
func (m *SingletonManager) Healthz(ctx context.Context) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.sandbox == nil {
		return fmt.Errorf("singleton sandbox not initialized")
	}

	return m.checkHealth(ctx, m.sandbox)
}

// Metrics 返回监控指标。
// Metrics 返回监控指标。健康探测在锁外执行（exec 最多阻塞数秒，
// 持锁探测会卡住 Acquire/Release），并带超时上界。
func (m *SingletonManager) Metrics() ManagerMetrics {
	m.mu.RLock()
	total := m.acquireTotal.Load()
	latency := m.acquireLatency.Load()
	sandbox := m.sandbox
	m.mu.RUnlock()

	avgLatencyMs := float64(0)
	if total > 0 {
		avgLatencyMs = float64(latency) / float64(total) / 1000.0 // 微秒 → 毫秒
	}

	healthyCount := 0
	if sandbox != nil {
		hctx, hcancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer hcancel()
		if err := m.checkHealth(hctx, sandbox); err == nil {
			healthyCount = 1
		}
	}

	return ManagerMetrics{
		TotalSandboxes:   1,
		HealthyCount:     healthyCount,
		AcquireLatencyMs: avgLatencyMs,
		AcquireTotal:     total,
		AcquireErrors:    m.acquireErrors.Load(),
		BusyPoolSize:     0, // 单例模式无池
		WarmPoolSize:     0,
		IdlePoolSize:     0,
		QueueLength:      0,
		UtilizationRatio: 0,
	}
}

// Shutdown 优雅关闭。
func (m *SingletonManager) Shutdown(ctx context.Context) error {
	m.logger.Info().Msg("shutting down singleton manager")

	// 停止后台 goroutine
	m.cancel()
	m.wg.Wait()

	// 销毁容器
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.sandbox != nil {
		shutdownCtx, cancel := context.WithTimeout(ctx, m.shutdownTimeout)
		defer cancel()

		if err := m.launcher.Destroy(shutdownCtx, m.sandbox.ID); err != nil {
			m.logger.Error().
				Err(err).
				Str("container_id", m.sandbox.ID).
				Msg("failed to destroy singleton sandbox")
			return err
		}

		m.logger.Info().
			Str("container_id", m.sandbox.ID).
			Msg("singleton sandbox destroyed")

		m.sandbox = nil
	}

	return nil
}

// healthCheckLoop 后台健康检查循环。
func (m *SingletonManager) healthCheckLoop() {
	defer m.wg.Done()

	ticker := time.NewTicker(m.healthCheckPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.performHealthCheck()
		}
	}
}

// performHealthCheck 执行健康检查，不健康时自动重启。
func (m *SingletonManager) performHealthCheck() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.sandbox == nil {
		return
	}

	ctx, cancel := context.WithTimeout(m.ctx, 10*time.Second)
	defer cancel()

	if err := m.checkHealth(ctx, m.sandbox); err != nil {
		m.logger.Warn().
			Err(err).
			Str("container_id", m.sandbox.ID).
			Msg("singleton sandbox unhealthy, restarting")

		// 销毁旧容器
		_ = m.launcher.Destroy(context.Background(), m.sandbox.ID)
		m.sandbox = nil

		// 创建新容器
		client, err := m.launcher.Spawn(context.Background(), "system-sandbox")
		if err != nil {
			m.logger.Error().
				Err(err).
				Msg("failed to restart singleton sandbox")
			return
		}

		m.sandbox = &Sandbox{
			Client:    client,
			ID:        "system-sandbox",
			CreatedAt: time.Now(),
		}

		m.logger.Info().
			Str("container_id", m.sandbox.ID).
			Msg("singleton sandbox restarted successfully")
	}
}

// checkHealth 检查容器健康状态。
func (m *SingletonManager) checkHealth(ctx context.Context, sb *Sandbox) error {
	// 执行简单命令测试容器是否响应
	_, err := sb.Client.Exec(ctx, ExecRequest{
		TaskID:         "healthcheck",
		AgentID:        "singleton-manager",
		Command:        "echo healthy",
		TimeoutSeconds: 5,
	})
	return err
}
