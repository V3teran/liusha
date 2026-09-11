package core

import (
	"context"
	"encoding/json"
	"fmt"
)

// StateWrapper 是状态包装器，提供便捷方法。
type StateWrapper[T any] struct {
	state   *State[T]
	manager StateManager[T]
}

// NewStateWrapper 创建状态包装器。
func NewStateWrapper[T any](taskID string, manager StateManager[T]) *StateWrapper[T] {
	return &StateWrapper[T]{
		state: &State[T]{
			TaskID: taskID,
		},
		manager: manager,
	}
}

// Get 获取当前状态数据。
func (w *StateWrapper[T]) Get(ctx context.Context) (*T, error) {
	state, err := w.manager.Get(ctx, w.state.TaskID)
	if err != nil {
		return nil, err
	}
	w.state = state
	return &state.Data, nil
}

// Update 更新状态数据。
func (w *StateWrapper[T]) Update(ctx context.Context, data T) error {
	w.state.Data = data
	return w.manager.Update(ctx, w.state)
}

// UpdateFunc 使用函数更新状态。
func (w *StateWrapper[T]) UpdateFunc(ctx context.Context, fn func(*T) error) error {
	data, err := w.Get(ctx)
	if err != nil {
		return err
	}

	if err := fn(data); err != nil {
		return err
	}

	return w.Update(ctx, *data)
}

// Snapshot 创建快照。
func (w *StateWrapper[T]) Snapshot() (json.RawMessage, error) {
	return w.state.Snapshot()
}

// Restore 从快照恢复。
func (w *StateWrapper[T]) Restore(snapshot json.RawMessage) error {
	restored, err := RestoreSnapshot[T](snapshot)
	if err != nil {
		return err
	}
	w.state = restored
	return nil
}

// SetMetadata 设置元数据（使用 Phase 字段）。
func (w *StateWrapper[T]) SetMetadata(key, value string) {
	// 简化实现：将 key-value 存储到 Phase 字段
	// 实际使用时可以扩展 Metadata 结构
	if key == "phase" {
		w.state.Metadata.Phase = value
	}
}

// GetMetadata 获取元数据。
func (w *StateWrapper[T]) GetMetadata(key string) (string, bool) {
	if key == "phase" {
		return w.state.Metadata.Phase, true
	}
	return "", false
}

// ============================================
// 批量状态管理
// ============================================

// StateRegistry 状态注册表（管理多个任务状态）。
type StateRegistry[T any] struct {
	manager  StateManager[T]
	wrappers map[string]*StateWrapper[T]
}

// NewStateRegistry 创建状态注册表。
func NewStateRegistry[T any](manager StateManager[T]) *StateRegistry[T] {
	return &StateRegistry[T]{
		manager:  manager,
		wrappers: make(map[string]*StateWrapper[T]),
	}
}

// Register 注册任务状态。
func (r *StateRegistry[T]) Register(taskID string) *StateWrapper[T] {
	wrapper := NewStateWrapper(taskID, r.manager)
	r.wrappers[taskID] = wrapper
	return wrapper
}

// Get 获取任务状态包装器。
func (r *StateRegistry[T]) Get(taskID string) (*StateWrapper[T], error) {
	wrapper, exists := r.wrappers[taskID]
	if !exists {
		return nil, fmt.Errorf("task not registered: %s", taskID)
	}
	return wrapper, nil
}

// ListTasks 列出所有任务。
func (r *StateRegistry[T]) ListTasks() []string {
	tasks := make([]string, 0, len(r.wrappers))
	for taskID := range r.wrappers {
		tasks = append(tasks, taskID)
	}
	return tasks
}

// ============================================
// 状态观察者
// ============================================

// StateObserver 状态观察者接口。
type StateObserver[T any] interface {
	OnStateChange(taskID string, oldData, newData T)
}

// ObservableStateWrapper 可观察的状态包装器。
type ObservableStateWrapper[T any] struct {
	*StateWrapper[T]
	observers []StateObserver[T]
}

// NewObservableStateWrapper 创建可观察的状态包装器。
func NewObservableStateWrapper[T any](taskID string, manager StateManager[T]) *ObservableStateWrapper[T] {
	return &ObservableStateWrapper[T]{
		StateWrapper: NewStateWrapper(taskID, manager),
		observers:    make([]StateObserver[T], 0),
	}
}

// AddObserver 添加观察者。
func (w *ObservableStateWrapper[T]) AddObserver(observer StateObserver[T]) {
	w.observers = append(w.observers, observer)
}

// Update 更新状态（通知观察者）。
func (w *ObservableStateWrapper[T]) Update(ctx context.Context, newData T) error {
	oldData := w.state.Data

	if err := w.StateWrapper.Update(ctx, newData); err != nil {
		return err
	}

	// 通知观察者
	for _, observer := range w.observers {
		observer.OnStateChange(w.state.TaskID, oldData, newData)
	}

	return nil
}
