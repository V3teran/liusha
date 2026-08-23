package worker

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/hibiken/asynq"
)

// 启动 miniredis 并构造一个 Client，测试结束自动 Close。
func newTestClient(t *testing.T) (*Client, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	c := NewClient(asynq.RedisClientOpt{Addr: mr.Addr()})
	t.Cleanup(func() { _ = c.Close() })
	return c, mr
}

func TestRole_Queue(t *testing.T) {
	if got := RoleExecutor.Queue(); got != QueueExecutor {
		t.Fatalf("RoleExecutor.Queue() = %q, want %q", got, QueueExecutor)
	}
	if got := RoleDispatch.Queue(); got != QueueDispatch {
		t.Fatalf("RoleDispatch.Queue() = %q, want %q", got, QueueDispatch)
	}
}

func TestClient_Enqueue_RoutesQueue(t *testing.T) {
	c, _ := newTestClient(t)

	id, q, err := c.Enqueue(
		context.Background(),
		RoleExecutor,
		Payload{ExecutorID: "task-1", TaskID: "task-1", Role: RoleExecutor},
	)
	if err != nil {
		t.Fatalf("Enqueue err = %v", err)
	}
	if id == "" {
		t.Fatalf("expected non-empty task id")
	}
	if q != QueueExecutor {
		t.Fatalf("queue = %q, want %q", q, QueueExecutor)
	}
}

func TestClient_Enqueue_DispatchQueue(t *testing.T) {
	c, _ := newTestClient(t)

	_, q, err := c.Enqueue(
		context.Background(),
		RoleDispatch,
		Payload{ExecutorID: "task-op-1", TaskID: "task-1", Role: RoleDispatch},
	)
	if err != nil {
		t.Fatalf("Enqueue err = %v", err)
	}
	if q != QueueDispatch {
		t.Fatalf("queue = %q, want %q", q, QueueDispatch)
	}
}

// 同 ExecutorID 第二次 Enqueue 必须报错（asynq 默认行为：ErrTaskIDConflict）。
func TestClient_Enqueue_Idempotent(t *testing.T) {
	c, _ := newTestClient(t)
	ctx := context.Background()
	p := Payload{ExecutorID: "dup-1", TaskID: "task-1", Role: RoleExecutor}

	if _, _, err := c.Enqueue(ctx, RoleExecutor, p); err != nil {
		t.Fatalf("first enqueue err = %v", err)
	}
	_, _, err := c.Enqueue(ctx, RoleExecutor, p)
	if err == nil {
		t.Fatalf("expected error on duplicate ExecutorID, got nil")
	}
	if !errors.Is(err, asynq.ErrTaskIDConflict) {
		t.Fatalf("expected ErrTaskIDConflict, got %v", err)
	}
}

func TestMux_Register_AndAsynqMux(t *testing.T) {
	m := NewMux()
	called := false
	m.Register(RoleExecutor, func(ctx context.Context, p Payload) error {
		called = true
		return nil
	})

	mux := m.AsynqMux()
	if mux == nil {
		t.Fatalf("AsynqMux() returned nil")
	}

	// 直接调用 ServeMux.ProcessTask 验证路由 + 反序列化。
	payloadBytes, err := json.Marshal(Payload{ExecutorID: "t1", Role: RoleExecutor})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	task := asynq.NewTask(TaskTypeRun, payloadBytes)
	if err := mux.ProcessTask(context.Background(), task); err != nil {
		t.Fatalf("ProcessTask err = %v", err)
	}
	if !called {
		t.Fatalf("main handler should have been called")
	}
}

// 未注册的 role 应返回 SkipRetry，避免无限重试。
func TestMux_UnknownRole_SkipsRetry(t *testing.T) {
	m := NewMux()
	mux := m.AsynqMux()

	payloadBytes, _ := json.Marshal(Payload{ExecutorID: "t1", Role: RoleDispatch})
	task := asynq.NewTask(TaskTypeRun, payloadBytes)

	err := mux.ProcessTask(context.Background(), task)
	if err == nil {
		t.Fatalf("expected SkipRetry error, got nil")
	}
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("expected SkipRetry, got %v", err)
	}
}

// 端到端：Enqueue → asynq.Server 处理 → handler 收到 Payload。
func TestEndToEnd_EnqueueAndProcess(t *testing.T) {
	c, mr := newTestClient(t)
	ctx := context.Background()

	var (
		mu       sync.Mutex
		received Payload
		done     = make(chan struct{}, 1)
	)

	m := NewMux()
	m.Register(RoleExecutor, func(_ context.Context, p Payload) error {
		mu.Lock()
		received = p
		mu.Unlock()
		select {
		case done <- struct{}{}:
		default:
		}
		return nil
	})

	srv := asynq.NewServer(
		asynq.RedisClientOpt{Addr: mr.Addr()},
		asynq.Config{
			Concurrency: 1,
			Queues:      map[string]int{QueueExecutor: 5, QueueDispatch: 1},
			Logger:      discardLogger{},
		},
	)
	go func() { _ = srv.Run(m.AsynqMux()) }()
	t.Cleanup(srv.Shutdown)

	want := Payload{ExecutorID: "e2e-1", TaskID: "task-e2e", Role: RoleExecutor}
	if _, _, err := c.Enqueue(ctx, RoleExecutor, want); err != nil {
		t.Fatalf("Enqueue err = %v", err)
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("timeout waiting for handler")
	}

	mu.Lock()
	got := received
	mu.Unlock()
	if got.ExecutorID != want.ExecutorID || got.Role != want.Role {
		t.Fatalf("received payload = %+v, want %+v", got, want)
	}
}

// 静默 asynq 内部日志，避免污染测试输出。
type discardLogger struct{}

func (discardLogger) Debug(args ...interface{}) {}
func (discardLogger) Info(args ...interface{})  {}
func (discardLogger) Warn(args ...interface{})  {}
func (discardLogger) Error(args ...interface{}) {}
func (discardLogger) Fatal(args ...interface{}) {}
