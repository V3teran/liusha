package core

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// InMemoryStateManager 是 StateManager 的内存实现（用于测试和简单场景）
type InMemoryStateManager[T any] struct {
	mu     sync.RWMutex
	states map[string]*State[T]
}

// NewInMemoryStateManager 创建内存状态管理器
func NewInMemoryStateManager[T any]() *InMemoryStateManager[T] {
	return &InMemoryStateManager[T]{
		states: make(map[string]*State[T]),
	}
}

// Get 获取任务状态
func (m *InMemoryStateManager[T]) Get(ctx context.Context, taskID string) (*State[T], error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	state, exists := m.states[taskID]
	if !exists {
		return nil, fmt.Errorf("任务状态不存在: %s", taskID)
	}

	// 返回深拷贝，防止外部修改
	return m.cloneState(state), nil
}

// Create 创建新任务状态
func (m *InMemoryStateManager[T]) Create(ctx context.Context, state *State[T]) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.states[state.TaskID]; exists {
		return fmt.Errorf("任务状态已存在: %s", state.TaskID)
	}

	now := time.Now().UnixMilli()
	state.Version = 1
	state.Metadata.CreatedAt = now
	state.Metadata.UpdatedAt = now

	m.states[state.TaskID] = m.cloneState(state)
	return nil
}

// Update 更新任务状态（乐观锁）
func (m *InMemoryStateManager[T]) Update(ctx context.Context, state *State[T]) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	existing, exists := m.states[state.TaskID]
	if !exists {
		return fmt.Errorf("任务状态不存在: %s", state.TaskID)
	}

	// 乐观锁检查
	if existing.Version != state.Version {
		return ErrVersionConflict{
			TaskID:          state.TaskID,
			ExpectedVersion: state.Version,
			ActualVersion:   existing.Version,
		}
	}

	// 更新版本和时间戳
	state.Version++
	state.Metadata.UpdatedAt = time.Now().UnixMilli()

	m.states[state.TaskID] = m.cloneState(state)
	return nil
}

// UpdateWith 使用 Reducer 更新状态（原子操作，自动处理并发冲突）
func (m *InMemoryStateManager[T]) UpdateWith(
	ctx context.Context,
	taskID string,
	newData T,
	reducer StateReducer[T],
) error {
	const maxRetries = 10
	var lastErr error

	for retry := 0; retry < maxRetries; retry++ {
		// 1. 读取当前状态
		oldState, err := m.Get(ctx, taskID)
		if err != nil {
			return fmt.Errorf("读取状态失败: %w", err)
		}

		// 2. 应用 Reducer
		mergedData, err := reducer.Reduce(oldState.Data, newData)
		if err != nil {
			return fmt.Errorf("应用 Reducer 失败 (%s): %w", reducer.Name(), err)
		}

		// 3. 构造新状态
		updatedState := &State[T]{
			TaskID:   taskID,
			Data:     mergedData,
			Metadata: oldState.Metadata,
			Version:  oldState.Version,
		}

		// 4. 乐观锁更新
		err = m.Update(ctx, updatedState)
		if err == nil {
			// 更新成功
			return nil
		}

		// 5. 检查是否是版本冲突
		if _, ok := err.(ErrVersionConflict); !ok {
			// 非版本冲突错误，直接返回
			return err
		}

		// 6. 版本冲突，重试
		lastErr = err
		time.Sleep(time.Millisecond * time.Duration(retry+1)) // 指数退避
	}

	return fmt.Errorf("更新状态失败，重试 %d 次后放弃: %w", maxRetries, lastErr)
}

// Delete 删除任务状态
func (m *InMemoryStateManager[T]) Delete(ctx context.Context, taskID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.states[taskID]; !exists {
		return fmt.Errorf("任务状态不存在: %s", taskID)
	}

	delete(m.states, taskID)
	return nil
}

// List 列出任务状态（分页）
func (m *InMemoryStateManager[T]) List(ctx context.Context, filter StateFilter) ([]*State[T], error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var results []*State[T]

	for _, state := range m.states {
		// 应用过滤器
		if filter.Phase != "" && state.Metadata.Phase != filter.Phase {
			continue
		}
		if filter.ParentTaskID != "" && state.Metadata.ParentTaskID != filter.ParentTaskID {
			continue
		}
		if len(filter.Tags) > 0 && !m.matchTags(state.Metadata.Tags, filter.Tags) {
			continue
		}

		results = append(results, m.cloneState(state))
	}

	// 应用分页
	start := filter.Offset
	if start > len(results) {
		return []*State[T]{}, nil
	}

	end := start + filter.Limit
	if filter.Limit == 0 || end > len(results) {
		end = len(results)
	}

	return results[start:end], nil
}

// cloneState 深拷贝状态（防止外部修改）
func (m *InMemoryStateManager[T]) cloneState(state *State[T]) *State[T] {
	if state == nil {
		return nil
	}

	cloned := &State[T]{
		TaskID:   state.TaskID,
		Data:     state.Data, // 注意：这里是浅拷贝，如果 T 包含引用类型需要业务层处理
		Version:  state.Version,
		Metadata: state.Metadata,
	}

	// 深拷贝 Tags
	if state.Metadata.Tags != nil {
		cloned.Metadata.Tags = make(map[string]string, len(state.Metadata.Tags))
		for k, v := range state.Metadata.Tags {
			cloned.Metadata.Tags[k] = v
		}
	}

	return cloned
}

// matchTags 检查标签是否匹配
func (m *InMemoryStateManager[T]) matchTags(stateTags, filterTags map[string]string) bool {
	for k, v := range filterTags {
		if stateTags[k] != v {
			return false
		}
	}
	return true
}
