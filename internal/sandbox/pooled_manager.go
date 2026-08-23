package sandbox

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

// PooledManager 是带引用计数的 Sandbox 池化管理器。
// 按 assignmentID 池化容器，多个 Task 共享同一容器。
// 引用计数归零后延迟清理（grace period），避免频繁创建销毁。
type PooledManager struct {
	launcher Launcher
	logger   zerolog.Logger

	mu      sync.RWMutex
	pools   map[string]*sandboxPool // assignmentID -> pool
	graceMs int                      // 引用计数归零后的保留时长（毫秒）
}

// sandboxPool 是单个 assignmentID 的容器池
type sandboxPool struct {
	client   Client
	refCount int
	timer    *time.Timer // 引用计数归零后的延迟清理定时器
}

// NewPooledManager 创建池化管理器
func NewPooledManager(launcher Launcher, logger zerolog.Logger, gracePeriodMs int) *PooledManager {
	if gracePeriodMs <= 0 {
		gracePeriodMs = 30000 // 默认 30 秒
	}

	return &PooledManager{
		launcher: launcher,
		logger:   logger,
		pools:    make(map[string]*sandboxPool),
		graceMs:  gracePeriodMs,
	}
}

// Acquire 获取或创建 Sandbox，增加引用计数
func (m *PooledManager) Acquire(ctx context.Context, assignmentID string) (Client, error) {
	if assignmentID == "" {
		return nil, fmt.Errorf("pooled sandbox: assignmentID 为空")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	pool, exists := m.pools[assignmentID]

	// 情况1：池存在且有活跃容器
	if exists && pool != nil {
		// 取消延迟清理定时器（如果有）
		if pool.timer != nil {
			pool.timer.Stop()
			pool.timer = nil
		}
		pool.refCount++
		m.logger.Debug().
			Str("assignment_id", assignmentID).
			Int("ref_count", pool.refCount).
			Msg("sandbox acquired (reuse)")
		return pool.client, nil
	}

	// 情况2：首次创建
	client, err := m.launcher.Spawn(ctx, assignmentID)
	if err != nil {
		return nil, fmt.Errorf("pooled sandbox: spawn failed: %w", err)
	}

	m.pools[assignmentID] = &sandboxPool{
		client:   client,
		refCount: 1,
		timer:    nil,
	}

	m.logger.Info().
		Str("assignment_id", assignmentID).
		Msg("sandbox acquired (new)")

	return client, nil
}

// Release 释放 Sandbox 引用，引用计数归零后启动延迟清理
func (m *PooledManager) Release(ctx context.Context, assignmentID string) error {
	if assignmentID == "" {
		return fmt.Errorf("pooled sandbox: assignmentID 为空")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	pool, exists := m.pools[assignmentID]
	if !exists || pool == nil {
		// 已被清理，幂等成功
		return nil
	}

	pool.refCount--

	if pool.refCount < 0 {
		m.logger.Error().
			Str("assignment_id", assignmentID).
			Int("ref_count", pool.refCount).
			Msg("sandbox ref_count < 0 (bug)")
		pool.refCount = 0
	}

	m.logger.Debug().
		Str("assignment_id", assignmentID).
		Int("ref_count", pool.refCount).
		Msg("sandbox released")

	// 引用计数归零：启动延迟清理
	if pool.refCount == 0 {
		pool.timer = time.AfterFunc(time.Duration(m.graceMs)*time.Millisecond, func() {
			m.destroyDeferred(assignmentID)
		})

		m.logger.Info().
			Str("assignment_id", assignmentID).
			Int("grace_ms", m.graceMs).
			Msg("sandbox ref_count=0, deferred cleanup scheduled")
	}

	return nil
}

// destroyDeferred 延迟清理回调（引用计数归零后的 grace period 结束）
func (m *PooledManager) destroyDeferred(assignmentID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	pool, exists := m.pools[assignmentID]
	if !exists || pool == nil {
		return
	}

	// 二次检查：可能在 grace period 内被重新 Acquire
	if pool.refCount > 0 {
		m.logger.Info().
			Str("assignment_id", assignmentID).
			Int("ref_count", pool.refCount).
			Msg("sandbox deferred cleanup cancelled (re-acquired)")
		return
	}

	// 销毁容器
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := m.launcher.Destroy(ctx, assignmentID); err != nil {
		m.logger.Error().
			Err(err).
			Str("assignment_id", assignmentID).
			Msg("sandbox destroy failed")
	} else {
		m.logger.Info().
			Str("assignment_id", assignmentID).
			Msg("sandbox destroyed (ref_count=0)")
	}

	delete(m.pools, assignmentID)
}

// DestroyAll 立即清理所有容器（进程退出时调用）
func (m *PooledManager) DestroyAll(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var firstErr error

	for assignmentID, pool := range m.pools {
		// 停止延迟清理定时器
		if pool.timer != nil {
			pool.timer.Stop()
		}

		// 销毁容器
		if err := m.launcher.Destroy(ctx, assignmentID); err != nil {
			m.logger.Error().
				Err(err).
				Str("assignment_id", assignmentID).
				Msg("sandbox destroy failed in DestroyAll")
			if firstErr == nil {
				firstErr = err
			}
		}
	}

	m.pools = make(map[string]*sandboxPool)

	return firstErr
}

// Stats 返回当前池状态（监控用）
func (m *PooledManager) Stats() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	active := 0
	idle := 0
	totalRefs := 0

	for _, pool := range m.pools {
		if pool.refCount > 0 {
			active++
			totalRefs += pool.refCount
		} else {
			idle++
		}
	}

	return map[string]interface{}{
		"total_pools":  len(m.pools),
		"active_pools": active,
		"idle_pools":   idle,
		"total_refs":   totalRefs,
	}
}
