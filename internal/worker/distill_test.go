package worker

import (
	"context"
	"testing"
	"time"

	"github.com/hibiken/asynq"
)

// 经验沉淀入独立队列，按任务幂等：同一任务的沉淀还在队列里时再投递不报错、也不重复入队。
func TestClient_EnqueueDistill_IdempotentPerTask(t *testing.T) {
	c, mr := newTestClient(t)
	ctx := context.Background()

	if err := c.EnqueueDistill(ctx, "task-1", time.Minute); err != nil {
		t.Fatalf("first enqueue err = %v", err)
	}
	if err := c.EnqueueDistill(ctx, "task-1", time.Minute); err != nil {
		t.Fatalf("重复投递同一任务的沉淀应视为已在队列: %v", err)
	}
	insp := asynq.NewInspector(asynq.RedisClientOpt{Addr: mr.Addr()})
	defer func() { _ = insp.Close() }()
	info, err := insp.GetTaskInfo(QueueDistill, distillTaskID("task-1"))
	if err != nil {
		t.Fatalf("沉淀任务应在 %s 队列: %v", QueueDistill, err)
	}
	if info.Type != TaskTypeDistill || info.MaxRetry != distillMaxRetry {
		t.Fatalf("type=%s maxRetry=%d", info.Type, info.MaxRetry)
	}
	if err := c.EnqueueDistill(ctx, "", time.Minute); err == nil {
		t.Fatal("空任务 ID 应报错")
	}
}

// 沉淀任务由 HandleDistill 注册的处理函数消费，拿到任务 ID。
func TestMux_DistillRoutesTaskID(t *testing.T) {
	mux := NewMux()
	got := make(chan string, 1)
	mux.HandleDistill(func(_ context.Context, taskID string) error {
		got <- taskID
		return nil
	})
	task := asynq.NewTask(TaskTypeDistill, []byte(`{"task_id":"task-9"}`))
	if err := mux.AsynqMux().ProcessTask(context.Background(), task); err != nil {
		t.Fatalf("ProcessTask err = %v", err)
	}
	if id := <-got; id != "task-9" {
		t.Fatalf("taskID = %q", id)
	}
}
