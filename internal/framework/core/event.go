package core

import (
	"context"
	"encoding/json"
	"time"
)

// Event 是框架统一的事件载体。
// 用于流式输出、日志、监控、审计。
type Event struct {
	// 事件 ID（全局唯一）
	ID string `json:"id"`

	// 事件类型
	Type EventType `json:"type"`

	// 任务 ID
	TaskID string `json:"task_id"`

	// 事件数据（业务自定义）
	Data json.RawMessage `json:"data"`

	// 事件来源（哪个组件产生）
	Source string `json:"source"`

	// 事件时间戳（Unix 毫秒）
	Timestamp int64 `json:"timestamp"`

	// 事件优先级
	Priority EventPriority `json:"priority"`

	// 关联的节点 ID（可选）
	NodeID string `json:"node_id,omitempty"`

	// 事件标签
	Labels map[string]string `json:"labels,omitempty"`
}

// EventType 是事件类型。
type EventType string

const (
	// 任务生命周期事件
	EventTaskCreated   EventType = "task.created"
	EventTaskStarted   EventType = "task.started"
	EventTaskCompleted EventType = "task.completed"
	EventTaskFailed    EventType = "task.failed"
	EventTaskCanceled  EventType = "task.canceled"

	// 阶段事件
	EventPhaseStarted   EventType = "phase.started"
	EventPhaseCompleted EventType = "phase.completed"

	// 节点事件
	EventNodeStarted   EventType = "node.started"
	EventNodeCompleted EventType = "node.completed"
	EventNodeFailed    EventType = "node.failed"
	EventNodeSkipped   EventType = "node.skipped"

	// Agent 事件
	EventAgentStarted EventType = "agent.started"
	EventAgentStopped EventType = "agent.stopped"
	EventAgentError   EventType = "agent.error"

	// 工具调用事件
	EventToolCallStarted   EventType = "tool.call.started"
	EventToolCallCompleted EventType = "tool.call.completed"
	EventToolCallFailed    EventType = "tool.call.failed"

	// LLM 调用事件
	EventLLMCallStarted   EventType = "llm.call.started"
	EventLLMCallCompleted EventType = "llm.call.completed"
	EventLLMCallStreaming EventType = "llm.call.streaming"

	// 人机交互事件
	EventHumanInputRequired EventType = "human.input.required"
	EventHumanInputReceived EventType = "human.input.received"

	// 检查点事件
	EventCheckpointCreated EventType = "checkpoint.created"
	EventCheckpointLoaded  EventType = "checkpoint.loaded"

	// 日志事件
	EventLog EventType = "log"

	// 自定义事件
	EventCustom EventType = "custom"
)

// EventPriority 是事件优先级。
type EventPriority int

const (
	PriorityLow      EventPriority = 0
	PriorityNormal   EventPriority = 1
	PriorityHigh     EventPriority = 2
	PriorityCritical EventPriority = 3
)

// EventPublisher 是事件发布器。
type EventPublisher interface {
	// Publish 发布事件
	Publish(ctx context.Context, event Event) error

	// PublishBatch 批量发布事件
	PublishBatch(ctx context.Context, events []Event) error
}

// EventSubscriber 是事件订阅器。
type EventSubscriber interface {
	// Subscribe 订阅事件（返回事件通道）
	Subscribe(ctx context.Context, filter EventFilter) (<-chan Event, error)

	// Unsubscribe 取消订阅
	Unsubscribe(subscriptionID string) error
}

// EventBus 是事件总线（发布订阅模式）。
type EventBus interface {
	EventPublisher
	EventSubscriber

	// Close 关闭事件总线
	Close() error
}

// EventFilter 是事件过滤器。
type EventFilter struct {
	// 按任务 ID 过滤
	TaskID string

	// 按事件类型过滤（支持通配符，如 "task.*"）
	Types []EventType

	// 按来源过滤
	Sources []string

	// 按优先级过滤（大于等于此优先级）
	MinPriority EventPriority

	// 按标签过滤
	Labels map[string]string

	// 时间范围过滤
	StartTime time.Time
	EndTime   time.Time
}

// EventHandler 是事件处理器（业务层实现）。
type EventHandler interface {
	// Handle 处理事件
	Handle(ctx context.Context, event Event) error

	// CanHandle 判断是否能处理此事件
	CanHandle(event Event) bool
}

// EventMiddleware 是事件中间件。
type EventMiddleware interface {
	// Before 在事件发布前调用
	Before(ctx context.Context, event *Event) error

	// After 在事件发布后调用
	After(ctx context.Context, event Event) error
}

// EventStore 是事件存储（持久化）。
type EventStore interface {
	// Save 保存事件
	Save(ctx context.Context, event Event) error

	// Query 查询事件
	Query(ctx context.Context, filter EventFilter, limit int) ([]Event, error)

	// Count 统计事件数量
	Count(ctx context.Context, filter EventFilter) (int64, error)

	// Delete 删除事件（清理过期数据）
	Delete(ctx context.Context, filter EventFilter) error
}

// EventStream 是事件流（SSE/WebSocket）。
type EventStream interface {
	// Stream 流式发送事件
	Stream(ctx context.Context, filter EventFilter) (<-chan Event, error)

	// Close 关闭流
	Close() error
}

// NewEvent 创建新事件（便捷函数）。
func NewEvent(eventType EventType, taskID, source string, data any) Event {
	dataJSON, _ := json.Marshal(data)
	return Event{
		Type:      eventType,
		TaskID:    taskID,
		Source:    source,
		Data:      dataJSON,
		Timestamp: time.Now().UnixMilli(),
		Priority:  PriorityNormal,
	}
}
