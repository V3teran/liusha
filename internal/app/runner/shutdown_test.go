package runner

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/hibiken/asynq"
	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/worker"
)

// 停机时在途 run 立即看到取消、按中断收尾，且收尾写入在 shutdown 返回前完成——
// 进程退出后才写就等于没写：run 残留 running，只能等僵尸回收并被计作一次出错。
func TestShutdown_InterruptsInFlightRunsAndWaitsForTheirWrapUp(t *testing.T) {
	mr := miniredis.RunT(t)
	opt := asynq.RedisClientOpt{Addr: mr.Addr()}
	cfg := config.RunnerConfig{AsynqConcurrency: 1, QueueAgentWeight: 1, AsynqShutdownTimeoutSeconds: 10}
	ts := newTaskServer(opt, cfg, zerolog.Nop())

	started := make(chan struct{})
	var observed atomic.Value
	var wrappedUp atomic.Bool
	mux := asynq.NewServeMux()
	mux.HandleFunc("run", func(ctx context.Context, _ *asynq.Task) error {
		close(started)
		<-ctx.Done()
		observed.Store(ctx.Err())
		time.Sleep(300 * time.Millisecond) // 终态回写、释放沙盒
		wrappedUp.Store(true)
		return nil
	})
	if err := ts.Start(mux); err != nil {
		t.Fatal(err)
	}
	client := asynq.NewClient(opt)
	defer func() { _ = client.Close() }()
	if _, err := client.Enqueue(asynq.NewTask("run", nil), asynq.Queue(worker.QueueAgent), asynq.MaxRetry(0)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("任务未被消费")
	}

	begin := time.Now()
	shutdown(zerolog.Nop(), cfg, ts, &http.Server{}, func() {})

	if err, _ := observed.Load().(error); !errors.Is(err, context.Canceled) {
		t.Fatalf("在途 run 看到的 ctx 错误 = %v，期望 context.Canceled（停机中断）", err)
	}
	if !wrappedUp.Load() {
		t.Fatal("shutdown 返回时在途 run 尚未收尾")
	}
	if elapsed := time.Since(begin); elapsed > 5*time.Second {
		t.Fatalf("停机耗时 %v：应立即中断在途 run，而不是等宽限期耗尽", elapsed)
	}
}
