package memory

import (
	"context"
	"sync"

	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/framework/persistence"
)

// MemoryStore 是内存实现的统一持久化存储。
// 组合四个组件：GraphStore、EventStore、StateManager、Checkpointer。
type MemoryStore struct {
	graph        *GraphStore
	events       *EventStore
	stateManager *StateManager[any]
	checkpointer *Checkpointer
	mu           sync.RWMutex
}

// New 创建内存存储实例
func New() *MemoryStore {
	return &MemoryStore{
		graph:        NewGraphStore(),
		events:       NewEventStore(),
		stateManager: NewStateManager[any](),
		checkpointer: NewCheckpointer(),
	}
}

// GraphStore 返回知识图谱存储
func (s *MemoryStore) GraphStore() persistence.GraphStore {
	return s.graph
}

// EventStore 返回事件流存储
func (s *MemoryStore) EventStore() persistence.EventStore {
	return s.events
}

// StateManager 返回状态管理器
func (s *MemoryStore) StateManager() core.StateManager[any] {
	return s.stateManager
}

// Checkpointer 返回检查点管理器
func (s *MemoryStore) Checkpointer() core.Checkpointer {
	return s.checkpointer
}

// Close 关闭存储（内存实现无需关闭）
func (s *MemoryStore) Close() error {
	return nil
}

// Health 健康检查（内存实现始终健康）
func (s *MemoryStore) Health(ctx context.Context) error {
	return nil
}
