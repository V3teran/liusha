package memory

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/V3teran/liusha/internal/framework/core"
)

// StateManager 是内存版状态管理器（用于开发和测试）。
type StateManager[T any] struct {
	mu     sync.RWMutex
	states map[string]*core.State[T]
}

// NewStateManager 创建内存版状态管理器。
func NewStateManager[T any]() *StateManager[T] {
	return &StateManager[T]{
		states: make(map[string]*core.State[T]),
	}
}

// Get 获取任务状态。
func (s *StateManager[T]) Get(ctx context.Context, taskID string) (*core.State[T], error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	state, exists := s.states[taskID]
	if !exists {
		return nil, core.ErrTaskNotFound{TaskID: taskID}
	}

	// 返回副本，避免外部修改
	copied := *state
	return &copied, nil
}

// Create 创建新任务状态。
func (s *StateManager[T]) Create(ctx context.Context, state *core.State[T]) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 检查是否已存在
	if _, exists := s.states[state.TaskID]; exists {
		return core.ErrInvalidState{From: "none", To: "create"}
	}

	// 初始化版本号
	state.Version = 0

	// 保存状态
	s.states[state.TaskID] = state

	return nil
}

// Update 更新任务状态（乐观锁）。
func (s *StateManager[T]) Update(ctx context.Context, state *core.State[T]) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, exists := s.states[state.TaskID]
	if !exists {
		return core.ErrTaskNotFound{TaskID: state.TaskID}
	}

	// 乐观锁检查
	if existing.Version != state.Version {
		return core.ErrVersionConflict{
			TaskID:          state.TaskID,
			ExpectedVersion: state.Version,
			ActualVersion:   existing.Version,
		}
	}

	// 增加版本号
	state.Version++

	// 更新状态
	s.states[state.TaskID] = state

	return nil
}

// Delete 删除任务状态。
func (s *StateManager[T]) Delete(ctx context.Context, taskID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.states[taskID]; !exists {
		return core.ErrTaskNotFound{TaskID: taskID}
	}

	delete(s.states, taskID)
	return nil
}

// List 列出任务状态（分页）。
func (s *StateManager[T]) List(ctx context.Context, filter core.StateFilter) ([]*core.State[T], error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*core.State[T]

	for _, state := range s.states {
		// 应用过滤器
		if filter.Phase != "" && state.Metadata.Phase != filter.Phase {
			continue
		}
		if filter.ParentTaskID != "" && state.Metadata.ParentTaskID != filter.ParentTaskID {
			continue
		}
		if len(filter.Tags) > 0 {
			match := true
			for k, v := range filter.Tags {
				if state.Metadata.Tags[k] != v {
					match = false
					break
				}
			}
			if !match {
				continue
			}
		}

		// 返回副本
		copied := *state
		result = append(result, &copied)
	}

	// 分页
	if filter.Offset > 0 {
		if filter.Offset >= len(result) {
			return []*core.State[T]{}, nil
		}
		result = result[filter.Offset:]
	}
	if filter.Limit > 0 && len(result) > filter.Limit {
		result = result[:filter.Limit]
	}

	return result, nil
}

// LoadFromSnapshot 从快照加载状态。
func (s *StateManager[T]) LoadFromSnapshot(ctx context.Context, taskID string, snapshot json.RawMessage) error {
	state, err := core.RestoreSnapshot[T](snapshot)
	if err != nil {
		return err
	}

	state.TaskID = taskID

	s.mu.Lock()
	defer s.mu.Unlock()

	s.states[taskID] = state
	return nil
}

// Clear 清空所有状态（仅测试用）。
func (s *StateManager[T]) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.states = make(map[string]*core.State[T])
}
