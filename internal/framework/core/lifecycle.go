package core

import (
	"context"
	"time"
)

// Lifecycle 管理组件的生命周期。
type Lifecycle struct {
	state     string // created, starting, running, stopping, stopped, failed
	startedAt time.Time
	stoppedAt time.Time
}

// NewLifecycle 创建生命周期管理器。
func NewLifecycle() *Lifecycle {
	return &Lifecycle{
		state: "created",
	}
}

// Start 启动组件。
func (l *Lifecycle) Start(ctx context.Context, starter func(ctx context.Context) error) error {
	if l.state != "created" && l.state != "stopped" {
		return ErrInvalidState{From: l.state, To: "starting"}
	}

	l.state = "starting"
	l.startedAt = time.Now()

	if err := starter(ctx); err != nil {
		l.state = "failed"
		return err
	}

	l.state = "running"
	return nil
}

// Stop 停止组件。
func (l *Lifecycle) Stop(ctx context.Context, stopper func(ctx context.Context) error) error {
	if l.state != "running" {
		return ErrInvalidState{From: l.state, To: "stopping"}
	}

	l.state = "stopping"

	if err := stopper(ctx); err != nil {
		l.state = "failed"
		return err
	}

	l.state = "stopped"
	l.stoppedAt = time.Now()
	return nil
}

// State 获取当前状态。
func (l *Lifecycle) State() string {
	return l.state
}

// IsRunning 判断是否正在运行。
func (l *Lifecycle) IsRunning() bool {
	return l.state == "running"
}

// Uptime 获取运行时间。
func (l *Lifecycle) Uptime() time.Duration {
	if l.startedAt.IsZero() {
		return 0
	}
	if l.state == "running" {
		return time.Since(l.startedAt)
	}
	if !l.stoppedAt.IsZero() {
		return l.stoppedAt.Sub(l.startedAt)
	}
	return 0
}

// LifecycleManager 管理多个组件的生命周期。
type LifecycleManager struct {
	components map[string]*Lifecycle
}

// NewLifecycleManager 创建生命周期管理器。
func NewLifecycleManager() *LifecycleManager {
	return &LifecycleManager{
		components: make(map[string]*Lifecycle),
	}
}

// Register 注册组件。
func (m *LifecycleManager) Register(name string) *Lifecycle {
	lc := NewLifecycle()
	m.components[name] = lc
	return lc
}

// Get 获取组件生命周期。
func (m *LifecycleManager) Get(name string) *Lifecycle {
	return m.components[name]
}

// StartAll 启动所有组件。
func (m *LifecycleManager) StartAll(ctx context.Context, starters map[string]func(context.Context) error) error {
	for name, starter := range starters {
		lc := m.components[name]
		if lc == nil {
			lc = m.Register(name)
		}
		if err := lc.Start(ctx, starter); err != nil {
			return err
		}
	}
	return nil
}

// StopAll 停止所有组件。
func (m *LifecycleManager) StopAll(ctx context.Context, stoppers map[string]func(context.Context) error) error {
	for name, stopper := range stoppers {
		lc := m.components[name]
		if lc == nil || !lc.IsRunning() {
			continue
		}
		if err := lc.Stop(ctx, stopper); err != nil {
			return err
		}
	}
	return nil
}

// Status 获取所有组件状态。
func (m *LifecycleManager) Status() map[string]string {
	status := make(map[string]string)
	for name, lc := range m.components {
		status[name] = lc.State()
	}
	return status
}
