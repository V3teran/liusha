package sandbox

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
)

// WarmPoolManager 是热池 Sandbox 管理器。
// 维护一个温热的容器，空闲 N 分钟后自动回收，下次任务按需重建。
// 适用场景：低并发、长任务、需要快速启动。
type WarmPoolManager struct {
	launcher Launcher
	logger   zerolog.Logger

	mu        sync.RWMutex
	sandbox   *Sandbox  // 当前容器（可能为空）
	lastUsed  time.Time // 最后使用时间（Release 时更新）
	createdAt time.Time // 创建时间
	active    int       // 并发持有计数（asynq 并发任务共享同一容器）

	// 监控指标
	acquireTotal   atomic.Int64
	acquireErrors  atomic.Int64
	acquireLatency atomic.Int64 // 累计延迟（微秒）
	createdTotal   atomic.Int64 // 累计创建容器数
	destroyedTotal atomic.Int64 // 累计销毁容器数

	// 配置
	idleTimeout       time.Duration // 空闲超时（默认 30 分钟）
	healthCheckPeriod time.Duration // 健康检查间隔
	shutdownTimeout   time.Duration // 关闭超时

	// 生命周期
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// WarmPoolConfig 是 WarmPoolManager 的配置。
type WarmPoolConfig struct {
	IdleTimeout       time.Duration // 空闲超时（默认 30 分钟）
	HealthCheckPeriod time.Duration // 健康检查间隔（默认 30 秒）
	ShutdownTimeout   time.Duration // 关闭超时（默认 30 秒）
}

// NewWarmPoolManager 创建热池管理器。
func NewWarmPoolManager(launcher Launcher, logger zerolog.Logger, cfg WarmPoolConfig) *WarmPoolManager {
	// 默认值
	if cfg.IdleTimeout == 0 {
		cfg.IdleTimeout = 30 * time.Minute
	}
	if cfg.HealthCheckPeriod == 0 {
		cfg.HealthCheckPeriod = 30 * time.Second
	}
	if cfg.ShutdownTimeout == 0 {
		cfg.ShutdownTimeout = 30 * time.Second
	}

	ctx, cancel := context.WithCancel(context.Background())

	m := &WarmPoolManager{
		launcher:          launcher,
		logger:            logger,
		idleTimeout:       cfg.IdleTimeout,
		healthCheckPeriod: cfg.HealthCheckPeriod,
		shutdownTimeout:   cfg.ShutdownTimeout,
		ctx:               ctx,
		cancel:            cancel,
	}

	// 启动后台维护 goroutine
	m.wg.Add(1)
	go m.maintenanceLoop()

	return m
}

// EnsureRunning 确保有一个预热的 Sandbox 容器。
// 此方法可选：不调用也能正常工作（首次 Acquire 时自动创建）。
func (m *WarmPoolManager) EnsureRunning(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 如果已有容器，检查健康
	if m.sandbox != nil {
		if err := m.checkHealth(ctx, m.sandbox); err == nil {
			m.logger.Info().
				Str("container_id", m.sandbox.ID).
				Msg("warm pool sandbox already running and healthy")
			return nil
		}

		// 不健康，销毁旧容器
		m.logger.Warn().
			Str("container_id", m.sandbox.ID).
			Msg("warm pool sandbox unhealthy, recreating")
		m.destroySandboxLocked()
	}

	// 创建新容器
	if err := m.createSandboxLocked(ctx); err != nil {
		return err
	}

	m.logger.Info().
		Str("container_id", m.sandbox.ID).
		Msg("warm pool sandbox created successfully")

	return nil
}

// Acquire 获取 Sandbox。
// 如果池中有容器，立即返回（0 延迟）；否则创建新容器（~1.5s）。
func (m *WarmPoolManager) Acquire(ctx context.Context, req AcquireRequest) (*Sandbox, error) {
	start := time.Now()
	defer func() {
		m.acquireTotal.Add(1)
		m.acquireLatency.Add(time.Since(start).Microseconds())
	}()

	m.mu.Lock()
	defer m.mu.Unlock()

	// 如果容器不存在或不健康，创建新的
	if m.sandbox == nil {
		if err := m.createSandboxLocked(ctx); err != nil {
			m.acquireErrors.Add(1)
			return nil, err
		}
	} else {
		// 快速健康检查
		if err := m.checkHealth(ctx, m.sandbox); err != nil {
			m.logger.Warn().
				Err(err).
				Str("container_id", m.sandbox.ID).
				Msg("sandbox unhealthy during acquire, recreating")
			m.destroySandboxLocked()
			if err := m.createSandboxLocked(ctx); err != nil {
				m.acquireErrors.Add(1)
				return nil, err
			}
		}
	}

	// 借出前卫生（idle 复用路径）：上一个 runner 若被强杀（pkill），其
	// Release→SoftReset 与浏览器清理钩子被跳过，daemon/chromium 跨轮累积
	// （实测：2GB 容器内 17 个 chromium 进程把内存吃满 → 新任务 chromium
	// 冷启 30s 超时，两级自愈也救不了资源耗尽）。仅当本 borrow 前 active==0
	//（容器空闲）才清——并发在跑的任务身份不受影响。
	wasIdle := m.active == 0
	m.active++
	if wasIdle {
		if err := m.sandbox.SoftReset(ctx); err != nil {
			m.logger.Warn().Err(err).Str("container_id", m.sandbox.ID).
				Msg("借出前 SoftReset 失败（best-effort，残留风险留给自愈）")
		} else {
			m.logger.Info().Str("container_id", m.sandbox.ID).
				Msg("空闲容器借出前已清理（浏览器/进程/临时文件残留）")
		}
	}

	// 准备任务工作目录（默认对齐 sandbox-server 约定 /liusha/<taskID>）
	sb := &Sandbox{
		Client:    m.sandbox.Client,
		ID:        m.sandbox.ID,
		TaskID:    req.TaskID,
		WorkDir:   req.WorkDir,
		CreatedAt: m.sandbox.CreatedAt,
	}

	if err := sb.PrepareWorkDir(ctx); err != nil {
		m.acquireErrors.Add(1)
		m.active--
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
		Str("container_id", sb.ID).
		Msg("warm pool sandbox acquired")

	return sb, nil
}

// Release 释放 Sandbox：SoftReset（清任务目录 + 杀残留进程，防跨任务数据泄漏）
// 后递减持有计数；计数归零才开始计算空闲超时。
func (m *WarmPoolManager) Release(ctx context.Context, sb *Sandbox) error {
	if sb == nil {
		return nil
	}

	if err := sb.SoftReset(ctx); err != nil {
		m.logger.Warn().
			Err(err).
			Str("task_id", sb.TaskID).
			Msg("soft reset 失败（跨任务残留风险，best-effort 继续）")
	}

	m.mu.Lock()
	if m.active > 0 {
		m.active--
	}
	m.lastUsed = time.Now()
	m.mu.Unlock()

	m.logger.Debug().
		Str("task_id", sb.TaskID).
		Msg("warm pool sandbox released (now idle)")

	return nil
}

// Healthz 健康检查。
func (m *WarmPoolManager) Healthz(ctx context.Context) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.sandbox == nil {
		return fmt.Errorf("warm pool sandbox not initialized")
	}

	return m.checkHealth(ctx, m.sandbox)
}

// Metrics 返回监控指标。
// 健康探测在锁外执行：exec 最多阻塞 5s，持锁探测会卡住 Acquire/Release。
func (m *WarmPoolManager) Metrics() ManagerMetrics {
	m.mu.RLock()
	total := m.acquireTotal.Load()
	latency := m.acquireLatency.Load()
	sandbox := m.sandbox
	active := m.active
	m.mu.RUnlock()

	avgLatencyMs := float64(0)
	if total > 0 {
		avgLatencyMs = float64(latency) / float64(total) / 1000.0 // 微秒 → 毫秒
	}

	healthyCount := 0
	totalSandboxes := 0
	if sandbox != nil {
		totalSandboxes = 1
		hctx, hcancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer hcancel()
		if err := m.checkHealth(hctx, sandbox); err == nil {
			healthyCount = 1
		}
	}

	busyCount := active
	idleCount := 0
	if sandbox != nil && active == 0 {
		idleCount = 1
	}

	return ManagerMetrics{
		TotalSandboxes:   totalSandboxes,
		HealthyCount:     healthyCount,
		AcquireLatencyMs: avgLatencyMs,
		AcquireTotal:     total,
		AcquireErrors:    m.acquireErrors.Load(),
		BusyPoolSize:     busyCount,
		WarmPoolSize:     0, // 热池模式无预热池
		IdlePoolSize:     idleCount,
		QueueLength:      0,
		UtilizationRatio: float64(busyCount) / float64(max(totalSandboxes, 1)),
		CreatedTotal:     m.createdTotal.Load(),
		DestroyedTotal:   m.destroyedTotal.Load(),
	}
}

// Shutdown 优雅关闭。
func (m *WarmPoolManager) Shutdown(ctx context.Context) error {
	m.logger.Info().Msg("shutting down warm pool manager")

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
				Msg("failed to destroy warm pool sandbox")
			return err
		}

		m.logger.Info().
			Str("container_id", m.sandbox.ID).
			Msg("warm pool sandbox destroyed")

		m.sandbox = nil
	}

	return nil
}

// maintenanceLoop 后台维护循环：健康检查 + 空闲回收。
func (m *WarmPoolManager) maintenanceLoop() {
	defer m.wg.Done()

	ticker := time.NewTicker(m.healthCheckPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.performMaintenance()
		}
	}
}

// performMaintenance 执行维护任务。
func (m *WarmPoolManager) performMaintenance() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.sandbox == nil {
		return
	}

	// 检查 1: 空闲超时回收（只在无持有者时检查）
	if m.active == 0 {
		idleDuration := time.Since(m.lastUsed)
		if idleDuration > m.idleTimeout {
			m.logger.Info().
				Dur("idle_duration", idleDuration).
				Str("container_id", m.sandbox.ID).
				Msg("sandbox idle timeout, destroying")
			m.destroySandboxLocked()
			return
		}
	}

	// 检查 2: 健康检查（无论是否使用中都检查）
	ctx, cancel := context.WithTimeout(m.ctx, 10*time.Second)
	defer cancel()

	if err := m.checkHealth(ctx, m.sandbox); err != nil {
		m.logger.Warn().
			Err(err).
			Str("container_id", m.sandbox.ID).
			Msg("sandbox unhealthy, will recreate on next acquire")
		// 不健康但仍有持有者：不立即销毁，等 Release 后再处理
		// 不健康且空闲：立即销毁
		if m.active == 0 {
			m.destroySandboxLocked()
		}
	}
}

// createSandboxLocked 创建容器（需持有锁）。
func (m *WarmPoolManager) createSandboxLocked(ctx context.Context) error {
	client, err := m.launcher.Spawn(ctx, "warm-sandbox")
	if err != nil {
		m.logger.Error().Err(err).Msg("failed to spawn warm pool sandbox")
		return fmt.Errorf("spawn warm pool sandbox: %w", err)
	}

	m.sandbox = &Sandbox{
		Client:    client,
		ID:        "warm-sandbox",
		CreatedAt: time.Now(),
	}
	m.createdAt = time.Now()
	m.lastUsed = time.Now()
	m.createdTotal.Add(1)

	m.logger.Info().
		Str("container_id", m.sandbox.ID).
		Msg("warm pool sandbox created")

	return nil
}

// destroySandboxLocked 销毁容器（需持有锁）。
func (m *WarmPoolManager) destroySandboxLocked() {
	if m.sandbox == nil {
		return
	}

	destroyCtx, destroyCancel := context.WithTimeout(context.Background(), m.shutdownTimeout)
	defer destroyCancel()
	if err := m.launcher.Destroy(destroyCtx, m.sandbox.ID); err != nil {
		m.logger.Error().
			Err(err).
			Str("container_id", m.sandbox.ID).
			Msg("failed to destroy sandbox")
	} else {
		m.logger.Info().
			Str("container_id", m.sandbox.ID).
			Msg("sandbox destroyed")
	}

	m.destroyedTotal.Add(1)
	m.sandbox = nil
}

// checkHealth 检查容器健康状态。
func (m *WarmPoolManager) checkHealth(ctx context.Context, sb *Sandbox) error {
	// 执行简单命令测试容器是否响应
	_, err := sb.Client.Exec(ctx, ExecRequest{
		TaskID:         "healthcheck",
		AgentID:        "warmpool-manager",
		Command:        "echo healthy",
		TimeoutSeconds: 5,
	})
	return err
}
