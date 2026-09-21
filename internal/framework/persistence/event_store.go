package persistence

import (
	"context"
	"time"

	"github.com/V3teran/liusha/internal/bus"
)

// EventStore 是事件流的持久化接口（append-only）。
// 负责 bus.Event 的追加和查询，不支持修改和删除。
type EventStore interface {
	// Append 追加事件（append-only，幂等）
	Append(ctx context.Context, event bus.Event) error

	// AppendBatch 批量追加事件（原子操作）
	AppendBatch(ctx context.Context, events []bus.Event) error

	// Load 加载事件（按 ActionID 查询）
	Load(ctx context.Context, actionID string) ([]bus.Event, error)

	// LoadByTask 加载任务的所有事件
	LoadByTask(ctx context.Context, taskID string) ([]bus.Event, error)

	// LoadByType 按事件类型加载
	LoadByType(ctx context.Context, typ bus.EventType) ([]bus.Event, error)

	// LoadByTimeRange 按时间范围加载
	LoadByTimeRange(ctx context.Context, start, end time.Time) ([]bus.Event, error)

	// LoadStream 加载事件流（支持分页）
	LoadStream(ctx context.Context, filter EventFilter) (*EventStream, error)
}

// EventFilter 是事件查询过滤器
type EventFilter struct {
	TaskID    *string          // 任务 ID
	ActionID  *string          // Action ID
	EventType *bus.EventType   // 事件类型
	StartTime *time.Time       // 开始时间
	EndTime   *time.Time       // 结束时间
	Limit     int              // 限制数量（0 表示无限制）
	Offset    int              // 偏移量
	OrderBy   EventOrderBy     // 排序方式
}

// EventOrderBy 是事件排序方式
type EventOrderBy string

const (
	EventOrderByTimeAsc  EventOrderBy = "time_asc"  // 按时间升序
	EventOrderByTimeDesc EventOrderBy = "time_desc" // 按时间降序
)

// EventStream 是事件流结果
type EventStream struct {
	Events     []bus.Event // 事件列表
	TotalCount int         // 总数量
	HasMore    bool        // 是否有更多
	NextOffset int         // 下一页偏移量
}
