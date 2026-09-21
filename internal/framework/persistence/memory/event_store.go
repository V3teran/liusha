package memory

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/V3teran/liusha/internal/bus"
	"github.com/V3teran/liusha/internal/framework/persistence"
)

// EventStore 是内存实现的事件流存储（append-only）
type EventStore struct {
	events []bus.Event // 所有事件（只追加，不修改）

	// 索引：加速查询
	eventsByAction map[string][]int // actionID -> event indices
	eventsByTask   map[string][]int // taskID -> event indices
	eventsByType   map[bus.EventType][]int // eventType -> event indices

	mu sync.RWMutex
}

// NewEventStore 创建内存事件存储
func NewEventStore() *EventStore {
	return &EventStore{
		events:         make([]bus.Event, 0, 1000),
		eventsByAction: make(map[string][]int),
		eventsByTask:   make(map[string][]int),
		eventsByType:   make(map[bus.EventType][]int),
	}
}

// Append 追加单个事件
func (s *EventStore) Append(ctx context.Context, event bus.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.appendLocked(event)
}

// AppendBatch 批量追加事件（原子操作）
func (s *EventStore) AppendBatch(ctx context.Context, events []bus.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, event := range events {
		if err := s.appendLocked(event); err != nil {
			return err
		}
	}

	return nil
}

// appendLocked 追加事件（需要持有锁）
func (s *EventStore) appendLocked(event bus.Event) error {
	// 设置时间戳
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	// 生成事件 ID（如果没有）
	if event.ID == "" {
		event.ID = fmt.Sprintf("evt_%d_%d", time.Now().UnixNano(), len(s.events))
	}

	// 追加到事件列表
	idx := len(s.events)
	s.events = append(s.events, event)

	// 更新索引
	if event.ActionID != "" {
		s.eventsByAction[event.ActionID] = append(s.eventsByAction[event.ActionID], idx)
	}
	if event.TaskID != "" {
		s.eventsByTask[event.TaskID] = append(s.eventsByTask[event.TaskID], idx)
	}
	if event.Type != "" {
		s.eventsByType[event.Type] = append(s.eventsByType[event.Type], idx)
	}

	return nil
}

// Load 加载指定 ActionID 的所有事件
func (s *EventStore) Load(ctx context.Context, actionID string) ([]bus.Event, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	indices, exists := s.eventsByAction[actionID]
	if !exists {
		return []bus.Event{}, nil
	}

	result := make([]bus.Event, 0, len(indices))
	for _, idx := range indices {
		result = append(result, s.events[idx])
	}

	return result, nil
}

// LoadByTask 加载指定任务的所有事件
func (s *EventStore) LoadByTask(ctx context.Context, taskID string) ([]bus.Event, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	indices, exists := s.eventsByTask[taskID]
	if !exists {
		return []bus.Event{}, nil
	}

	result := make([]bus.Event, 0, len(indices))
	for _, idx := range indices {
		result = append(result, s.events[idx])
	}

	return result, nil
}

// LoadByType 加载指定类型的所有事件
func (s *EventStore) LoadByType(ctx context.Context, typ bus.EventType) ([]bus.Event, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	indices, exists := s.eventsByType[typ]
	if !exists {
		return []bus.Event{}, nil
	}

	result := make([]bus.Event, 0, len(indices))
	for _, idx := range indices {
		result = append(result, s.events[idx])
	}

	return result, nil
}

// LoadByTimeRange 加载指定时间范围的事件
func (s *EventStore) LoadByTimeRange(ctx context.Context, start, end time.Time) ([]bus.Event, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []bus.Event
	for _, event := range s.events {
		if !event.Timestamp.Before(start) && !event.Timestamp.After(end) {
			result = append(result, event)
		}
	}

	return result, nil
}

// LoadStream 加载事件流（支持复杂过滤和分页）
func (s *EventStore) LoadStream(ctx context.Context, filter persistence.EventFilter) (*persistence.EventStream, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// 过滤事件
	var filtered []bus.Event
	for _, event := range s.events {
		if s.matchEventFilter(event, filter) {
			filtered = append(filtered, event)
		}
	}

	// 排序
	if filter.OrderBy == persistence.EventOrderByTimeDesc {
		// 反转数组（降序）
		for i, j := 0, len(filtered)-1; i < j; i, j = i+1, j-1 {
			filtered[i], filtered[j] = filtered[j], filtered[i]
		}
	}

	totalCount := len(filtered)

	// 分页
	if filter.Offset > 0 {
		if filter.Offset >= len(filtered) {
			return &persistence.EventStream{
				Events:     []bus.Event{},
				TotalCount: totalCount,
				HasMore:    false,
				NextOffset: 0,
			}, nil
		}
		filtered = filtered[filter.Offset:]
	}

	hasMore := false
	nextOffset := 0
	if filter.Limit > 0 && len(filtered) > filter.Limit {
		filtered = filtered[:filter.Limit]
		hasMore = true
		nextOffset = filter.Offset + filter.Limit
	}

	return &persistence.EventStream{
		Events:     filtered,
		TotalCount: totalCount,
		HasMore:    hasMore,
		NextOffset: nextOffset,
	}, nil
}

// matchEventFilter 检查事件是否匹配过滤器
func (s *EventStore) matchEventFilter(event bus.Event, filter persistence.EventFilter) bool {
	if filter.TaskID != nil && event.TaskID != *filter.TaskID {
		return false
	}
	if filter.ActionID != nil && event.ActionID != *filter.ActionID {
		return false
	}
	if filter.EventType != nil && event.Type != *filter.EventType {
		return false
	}
	if filter.StartTime != nil && event.Timestamp.Before(*filter.StartTime) {
		return false
	}
	if filter.EndTime != nil && event.Timestamp.After(*filter.EndTime) {
		return false
	}
	return true
}
