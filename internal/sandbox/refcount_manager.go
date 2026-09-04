package sandbox

import (
	"context"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

// RefCountManager 增强版引用计数管理器，添加超时清理和监控。
type RefCountManager struct {
	poolMgr *PooledManager
	logger  zerolog.Logger

	mu            sync.RWMutex
	lastActivity  map[string]time.Time // assignmentID -> 最后活动时间
	containerTTL  time.Duration        // 容器最大存活时间
	cleanupTicker *time.Ticker
	stopCh        chan struct{}
}

// NewRefCountManager 创建增强版管理器
func NewRefCountManager(poolMgr *PooledManager, logger zerolog.Logger, containerTTL time.Duration) *RefCountManager {
	if containerTTL == 0 {
		containerTTL = 2 * time.Hour // 默认 2 小时
	}

	mgr := &RefCountManager{
		poolMgr:      poolMgr,
		logger:       logger.With().Str("component", "refcount_mgr").Logger(),
		lastActivity: make(map[string]time.Time),
		containerTTL: containerTTL,
		stopCh:       make(chan struct{}),
	}

	// 启动后台清理器（每 5 分钟检查一次）
	mgr.cleanupTicker = time.NewTicker(5 * time.Minute)
	go mgr.cleanupLoop()

	return mgr
}

// Acquire 分配容器并记录活动时间
func (m *RefCountManager) Acquire(ctx context.Context, assignmentID string) (Client, error) {
	client, err := m.poolMgr.Acquire(ctx, assignmentID)
	if err != nil {
		return nil, err
	}

	// 记录活动时间
	m.mu.Lock()
	m.lastActivity[assignmentID] = time.Now()
	m.mu.Unlock()

	return client, nil
}

// Release 释放容器
func (m *RefCountManager) Release(ctx context.Context, assignmentID string) error {
	err := m.poolMgr.Release(ctx, assignmentID)

	// 更新活动时间
	m.mu.Lock()
	m.lastActivity[assignmentID] = time.Now()
	m.mu.Unlock()

	return err
}

// cleanupLoop 后台清理循环
func (m *RefCountManager) cleanupLoop() {
	for {
		select {
		case <-m.cleanupTicker.C:
			m.cleanupStaleContainers()

		case <-m.stopCh:
			m.cleanupTicker.Stop()
			return
		}
	}
}

// cleanupStaleContainers 清理超时容器
func (m *RefCountManager) cleanupStaleContainers() {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	var stale []string

	// 查找超时容器
	for assignmentID, lastActive := range m.lastActivity {
		if now.Sub(lastActive) > m.containerTTL {
			stale = append(stale, assignmentID)
		}
	}

	if len(stale) == 0 {
		return
	}

	m.logger.Info().
		Int("count", len(stale)).
		Dur("ttl", m.containerTTL).
		Msg("cleaning up stale containers")

	// 强制清理超时容器
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	for _, assignmentID := range stale {
		// 直接调用 launcher.Destroy（绕过引用计数）
		if err := m.poolMgr.launcher.Destroy(ctx, assignmentID); err != nil {
			m.logger.Error().
				Err(err).
				Str("assignment_id", assignmentID).
				Msg("force destroy stale container failed")
		} else {
			m.logger.Info().
				Str("assignment_id", assignmentID).
				Msg("stale container destroyed")
			delete(m.lastActivity, assignmentID)
		}

		// 从 poolMgr 中移除
		m.poolMgr.mu.Lock()
		if pool, exists := m.poolMgr.pools[assignmentID]; exists {
			if pool.timer != nil {
				pool.timer.Stop()
			}
			delete(m.poolMgr.pools, assignmentID)
		}
		m.poolMgr.mu.Unlock()
	}
}

// Stop 停止后台清理器
func (m *RefCountManager) Stop() {
	close(m.stopCh)
}

// DestroyAll 清理所有容器
func (m *RefCountManager) DestroyAll(ctx context.Context) error {
	m.Stop()

	m.mu.Lock()
	m.lastActivity = make(map[string]time.Time)
	m.mu.Unlock()

	return m.poolMgr.DestroyAll(ctx)
}

// Stats 返回当前统计信息
func (m *RefCountManager) Stats() Stats {
	m.mu.RLock()
	defer m.mu.RUnlock()

	m.poolMgr.mu.RLock()
	defer m.poolMgr.mu.RUnlock()

	stats := Stats{
		TotalContainers: len(m.poolMgr.pools),
		ActiveCount:     0,
		IdleCount:       0,
	}

	for assignmentID, pool := range m.poolMgr.pools {
		if pool.refCount > 0 {
			stats.ActiveCount++
		} else {
			stats.IdleCount++
		}

		if lastActive, ok := m.lastActivity[assignmentID]; ok {
			age := time.Since(lastActive)
			if age > stats.OldestIdleAge {
				stats.OldestIdleAge = age
			}
		}
	}

	return stats
}

// Stats 容器统计信息
type Stats struct {
	TotalContainers int           // 总容器数
	ActiveCount     int           // 活跃容器数（refCount > 0）
	IdleCount       int           // 空闲容器数（refCount = 0）
	OldestIdleAge   time.Duration // 最老的空闲容器年龄
}
