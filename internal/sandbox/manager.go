package sandbox

import (
	"context"
	"fmt"
	"sync"
)

// Manager 管理 per-Assignment Sandbox 生命周期，支持多 Task 共享同一容器。
//
// 核心语义：一个 Assignment 一个 Sandbox 容器，同一 Assignment 的多个 Task 复用。
// GetOrSpawn 幂等创建，Destroy 显式销毁，DestroyAll 清理所有（进程退出时）。
type Manager struct {
	launcher Launcher
	mu       sync.RWMutex
	active   map[string]Client // assignmentID -> Client
}

// NewManager 构造 Manager。
func NewManager(launcher Launcher) *Manager {
	return &Manager{
		launcher: launcher,
		active:   make(map[string]Client),
	}
}

// GetOrSpawn 获取或创建指定 Assignment 的 Sandbox。
//
// 首次调用创建容器（容器名 = liusha-sandbox-{assignmentID}），后续调用返回缓存 Client。
// 并发安全：多个 Task 同时请求同一 Assignment 只创建一次容器。
func (m *Manager) GetOrSpawn(ctx context.Context, assignmentID string) (Client, error) {
	if assignmentID == "" {
		return nil, fmt.Errorf("sandbox.Manager: assignmentID 为空")
	}

	// 快路径：已存在直接返回
	m.mu.RLock()
	if client, ok := m.active[assignmentID]; ok {
		m.mu.RUnlock()
		return client, nil
	}
	m.mu.RUnlock()

	// 慢路径：加写锁创建（double-check 防并发重复创建）
	m.mu.Lock()
	defer m.mu.Unlock()

	if client, ok := m.active[assignmentID]; ok {
		return client, nil
	}

	client, err := m.launcher.Spawn(ctx, assignmentID)
	if err != nil {
		return nil, fmt.Errorf("sandbox.Manager: Spawn(%s) 失败: %w", assignmentID, err)
	}

	m.active[assignmentID] = client
	return client, nil
}

// Destroy 销毁指定 Assignment 的 Sandbox 容器。
//
// 从缓存删除并调用 launcher.Destroy。幂等：重复调用不报错。
// 用于手动清理或 Assignment 明确完成时。
func (m *Manager) Destroy(ctx context.Context, assignmentID string) error {
	if assignmentID == "" {
		return fmt.Errorf("sandbox.Manager: assignmentID 为空")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.active[assignmentID]; !ok {
		return nil // 已不存在，幂等成功
	}

	delete(m.active, assignmentID)

	if err := m.launcher.Destroy(ctx, assignmentID); err != nil {
		return fmt.Errorf("sandbox.Manager: Destroy(%s) 失败: %w", assignmentID, err)
	}

	return nil
}

// DestroyAll 清理所有活跃 Sandbox 容器。
//
// 进程退出时调用，best-effort 清理所有缓存容器。单个失败不阻塞后续。
// 返回第一个遇到的错误（如有），但会继续尝试清理所有容器。
func (m *Manager) DestroyAll(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var firstErr error
	for assignmentID := range m.active {
		if err := m.launcher.Destroy(ctx, assignmentID); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("sandbox.Manager: DestroyAll 失败于 %s: %w", assignmentID, err)
		}
		delete(m.active, assignmentID)
	}

	return firstErr
}

// Count 返回当前活跃 Sandbox 数量（调试/监控用）。
func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.active)
}
