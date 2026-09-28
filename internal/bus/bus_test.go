package bus

import (
	"context"
	"testing"
	"time"
)

// MemoryBus 是全平台事件骨干：Task 级路由 / Action 级路由 / 背压丢弃 / 关停语义。

func TestSubscribeTask_ReceivesPublishedEvent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b := New(ctx)

	sub := b.SubscribeTask("t1")
	b.PublishForTest("t1")

	select {
	case ev := <-sub.Events():
		if ev.TaskID != "t1" {
			t.Fatalf("event task_id = %q, want t1", ev.TaskID)
		}
		if ev.ID == "" || ev.Timestamp.IsZero() {
			t.Fatal("事件应被自动补上 ID 与时间戳")
		}
	case <-time.After(time.Second):
		t.Fatal("订阅者应收不到事件超时")
	}
}

func TestSubscribeTask_Isolation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b := New(ctx)

	subA := b.SubscribeTask("task-a")
	_ = b.SubscribeTask("task-b")
	b.Publish(Event{Type: EventActionProposed, TaskID: "task-b", ActionID: "act-1"})

	select {
	case ev := <-subA.Events():
		t.Fatalf("task-a 订阅不应收到 task-b 的事件，got %+v", ev)
	default:
	}
}

func TestUnsubscribeTask_StopsDelivery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b := New(ctx)

	sub := b.SubscribeTask("t1")
	sub.Cancel()
	b.Publish(Event{Type: EventActionCompleted, TaskID: "t1", ActionID: "a1"})

	// 取消订阅后通道已关闭或不再投递
	select {
	case ev, ok := <-sub.Events():
		if ok {
			t.Fatalf("取消订阅后不应再收到事件，got %+v", ev)
		}
	case <-time.After(200 * time.Millisecond):
		// 未收到即通过（通道可能被关闭也可能静默）
	}
}

func TestSubscribeAction_RoutesByActionID(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b := New(ctx)
	actx, acancel := context.WithCancel(ctx)
	defer acancel()

	sub := b.SubscribeAction(actx, "act-9")
	b.Publish(Event{Type: EventActionProposed, TaskID: "t1", ActionID: "act-9"})

	select {
	case ev := <-sub.Events():
		if ev.ActionID != "act-9" {
			t.Fatalf("action 路由错误: %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("action 订阅者超时")
	}
	sub.Unsubscribe()
}

func TestPublish_DoesNotBlockWhenNoSubscriber(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b := New(ctx)

	done := make(chan struct{})
	go func() {
		defer close(done)
		b.Publish(Event{Type: EventActionCompleted, TaskID: "nobody", ActionID: "a"})
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("无订阅者时 Publish 不应阻塞")
	}
}

func (b *MemoryBus) PublishForTest(taskID string) {
	b.Publish(Event{Type: "test.event", TaskID: taskID})
}

// TestSubscribeTask_Broadcast：同一 task 的多个订阅者（四智能体场景）
// 必须各自收到全部事件拷贝——单通道独吞是本轮修复的 P0。
func TestSubscribeTask_Broadcast(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b := New(ctx)

	subs := make([]*TaskSubscription, 4)
	for i := range subs {
		subs[i] = b.SubscribeTask("t1")
	}
	b.Publish(Event{Type: EventAttemptGenerated, TaskID: "t1", ActionID: "a1"})
	b.Publish(Event{Type: EventVerificationPassed, TaskID: "t1"})

	for i, s := range subs {
		got := 0
		for range 2 {
			select {
			case <-s.Events():
				got++
			case <-time.After(time.Second):
				t.Fatalf("订阅者 %d 只收到 %d/2 个事件（广播语义被破坏）", i, got)
			}
		}
	}

	// 单个订阅者退出不影响同伴
	subs[0].Cancel()
	b.Publish(Event{Type: EventActionCompleted, TaskID: "t1", ActionID: "a2"})
	select {
	case <-subs[1].Events():
	case <-time.After(time.Second):
		t.Fatal("订阅者 0 退出后，订阅者 1 仍应收到事件")
	}
}
