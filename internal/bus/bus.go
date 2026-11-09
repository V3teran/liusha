// Package bus 提供统一的事件总线实现。
//
// 语义：Task 级事件按**广播**分发——同一 task 的每个订阅者（planner/executor/
// evaluator/monitor/完成检测器）各自持有独立 channel，收到全部事件拷贝；
// 任一订阅者的消费速度不影响其他订阅者（通道满即丢弃，背压不级联）。
package bus

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/V3teran/liusha/internal/constants"
	"github.com/V3teran/liusha/internal/logx"
)

// EventBus 统一事件总线接口
type EventBus interface {
	// Task 级别订阅（广播语义，返回独立订阅句柄）
	SubscribeTask(taskID string) *TaskSubscription

	// 发布事件
	Publish(event Event)

	// 便捷方法
	PublishActionProposed(taskID, actionID string)
	PublishActionCompleted(taskID, actionID string)
	PublishAttemptGenerated(taskID, actionID string, attempt interface{})
	PublishVerificationPassed(taskID, nodeID string)
	PublishVerificationRefuted(taskID, actionID string)
	PublishTaskConverged(taskID, reason string)
	PublishReplanRequested(taskID, reason string)
}

// TaskSubscription 是 Task 级订阅句柄：持有独立的广播 channel。
type TaskSubscription struct {
	bus    *MemoryBus
	id     uint64
	taskID string
	events chan Event
}

// Events 返回该订阅者的事件通道
func (s *TaskSubscription) Events() <-chan Event { return s.events }

// Cancel 注销本订阅并关闭其事件通道（幂等；不影响同 task 其他订阅者）。
// 同步生效：返回后不会再有新事件投递到本订阅，已缓冲的事件仍可读出。
func (s *TaskSubscription) Cancel() {
	s.bus.submit(taskSubOp{kind: opUnsubscribe, sub: s})
}

type taskSubOpKind int

const (
	opSubscribe taskSubOpKind = iota
	opUnsubscribe
)

// taskSubOp 是 run() 内订阅表的操作请求（订阅表只在 run() 单 goroutine 变更，无竞争）。
type taskSubOp struct {
	kind taskSubOpKind
	sub  *TaskSubscription
	done chan struct{} // 操作完成信号
}

// MemoryBus 内存实现的事件总线。
type MemoryBus struct {
	ctx context.Context

	// Task 级订阅表（广播）：taskID → subscriberID → channel
	taskSubs    map[string]map[uint64]chan Event
	nextTaskSub atomic.Uint64
	taskOps     chan taskSubOp

	// 发布通道
	publish chan Event
}

// New 创建内存事件总线
func New(ctx context.Context) *MemoryBus {
	bus := &MemoryBus{
		ctx:      ctx,
		taskSubs: make(map[string]map[uint64]chan Event),
		taskOps:  make(chan taskSubOp, constants.ChannelBufferLarge),
		publish:  make(chan Event, constants.ChannelBufferVeryLarge),
	}
	go bus.run()
	return bus
}

// run 运行事件总线的主循环：订阅表的唯一变更点。
// 单轮分发 panic 被就地恢复（记日志后继续），避免一条坏事件永久失能总线——
// bus ctx 通常是 context.Background（进程级），循环一旦退出 Publish/Subscribe
// 会永久阻塞，四个 agent 随之死锁。
func (b *MemoryBus) run() {
	for {
		select {
		case <-b.ctx.Done():
			for _, subs := range b.taskSubs {
				for _, ch := range subs {
					close(ch)
				}
			}
			return

		case op := <-b.taskOps:
			b.safeHandleTaskOp(op)

		case event := <-b.publish:
			b.safeDispatch(event)
		}
	}
}

// safeHandleTaskOp 单次订阅表操作的 panic 隔离；无论成败都关闭 done，调用方不会挂住。
func (b *MemoryBus) safeHandleTaskOp(op taskSubOp) {
	defer close(op.done)
	defer logx.Recover("bus", "订阅表操作 panic")
	b.handleTaskOp(op)
}

// safeDispatch 单次事件分发的 panic 隔离。
func (b *MemoryBus) safeDispatch(event Event) {
	defer logx.Recover("bus", "事件分发 panic")

	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}
	if event.ID == "" {
		event.ID = generateEventID()
	}

	// Task 级广播：每个订阅者一份拷贝，满即丢弃（背压不级联）
	if event.TaskID != "" {
		for _, ch := range b.taskSubs[event.TaskID] {
			select {
			case ch <- event:
			default:
			}
		}
	}
}

// handleTaskOp 在 run() 内执行订阅表操作。done 由 safeHandleTaskOp 负责关闭。
func (b *MemoryBus) handleTaskOp(op taskSubOp) {
	sub := op.sub
	switch op.kind {
	case opSubscribe:
		subs, ok := b.taskSubs[sub.taskID]
		if !ok {
			subs = make(map[uint64]chan Event)
			b.taskSubs[sub.taskID] = subs
		}
		subs[sub.id] = sub.events
	case opUnsubscribe:
		subs, ok := b.taskSubs[sub.taskID]
		if !ok {
			return
		}
		if ch, ok := subs[sub.id]; ok {
			close(ch)
			delete(subs, sub.id)
		}
		if len(subs) == 0 {
			delete(b.taskSubs, sub.taskID)
		}
	}
}

// submit 把订阅表操作交给 run() 并等待其完成，使注册 / 注销对调用方同步生效。
// 入队后总线可能先于处理本操作而停止（ctx 取消，run 不再消费队列）：
// 确认等待必须同样监听总线 ctx，否则 done 永不关闭、调用方永久挂住。
func (b *MemoryBus) submit(op taskSubOp) {
	op.done = make(chan struct{})
	select {
	case b.taskOps <- op:
		select {
		case <-op.done:
		case <-b.ctx.Done():
		}
	case <-b.ctx.Done():
	}
}

// SubscribeTask 订阅指定 task 的全部事件（广播：独立 channel，收全量拷贝）。
// 注册同步完成：返回后立即开始接收事件。
func (b *MemoryBus) SubscribeTask(taskID string) *TaskSubscription {
	sub := &TaskSubscription{
		bus:    b,
		id:     b.nextTaskSub.Add(1),
		taskID: taskID,
		events: make(chan Event, constants.ChannelBufferLarge),
	}
	b.submit(taskSubOp{kind: opSubscribe, sub: sub})
	return sub
}

// Publish 发布事件
func (b *MemoryBus) Publish(event Event) {
	select {
	case b.publish <- event:
	case <-b.ctx.Done():
	}
}

// ============================================
// 便捷方法实现
// ============================================

// PublishActionProposed 发布 action 提议事件（planner 消费触发认领）。
func (b *MemoryBus) PublishActionProposed(taskID, actionID string) {
	b.Publish(Event{
		Type:     EventActionProposed,
		TaskID:   taskID,
		ActionID: actionID,
		Payload: map[string]interface{}{
			"action_id": actionID,
		},
	})
}

// PublishActionCompleted 发布 action 完成事件（detector 计步 / planner 重规划）。
func (b *MemoryBus) PublishActionCompleted(taskID, actionID string) {
	b.Publish(Event{
		Type:     EventActionCompleted,
		TaskID:   taskID,
		ActionID: actionID,
		Payload: map[string]interface{}{
			"action_id": actionID,
		},
	})
}

// PublishAttemptGenerated 发布漏洞候选事件（evaluator 复现门消费）。
func (b *MemoryBus) PublishAttemptGenerated(taskID, actionID string, attempt interface{}) {
	b.Publish(Event{
		Type:     EventAttemptGenerated,
		TaskID:   taskID,
		ActionID: actionID,
		Payload: map[string]interface{}{
			"action_id": actionID,
			"attempt":   attempt,
		},
	})
}

// PublishVerificationPassed 发布复现坐实事件（planner 重规划依据）。
func (b *MemoryBus) PublishVerificationPassed(taskID, nodeID string) {
	b.Publish(Event{
		Type:   EventVerificationPassed,
		TaskID: taskID,
		Payload: map[string]interface{}{
			"node_id": nodeID,
		},
	})
}

// PublishVerificationRefuted 发布复现证伪事件（planner 调整方向依据）。
func (b *MemoryBus) PublishVerificationRefuted(taskID, actionID string) {
	b.Publish(Event{
		Type:     EventVerificationRefuted,
		TaskID:   taskID,
		ActionID: actionID,
		Payload: map[string]interface{}{
			"action_id": actionID,
		},
	})
}

// PublishTaskConverged 发布任务收敛事件（planner 无新 action 可生成）。
// CompletionDetector 消费此事件以停止任务，避免空转 LLM 调用。
func (b *MemoryBus) PublishTaskConverged(taskID, reason string) {
	b.Publish(Event{
		Type:   EventTaskConverged,
		TaskID: taskID,
		Payload: map[string]interface{}{
			"reason": reason,
		},
	})
}

// PublishReplanRequested 发布重规划请求事件（monitor 触发）。
// planner 立即响应并强制重规划，跳过 executable action 检查。
func (b *MemoryBus) PublishReplanRequested(taskID, reason string) {
	b.Publish(Event{
		Type:   EventReplanRequested,
		TaskID: taskID,
		Payload: map[string]interface{}{
			"reason": reason,
		},
	})
}
