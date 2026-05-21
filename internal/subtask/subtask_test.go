package subtask

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// TestHandle_MarkDoneOnce 验证 MarkDone 首次生效，再次调用不覆盖。
func TestHandle_MarkDoneOnce(t *testing.T) {
	h := newHandle("tid-1", "测 XSS")
	if !h.IsRunning() {
		t.Fatalf("new handle 应是 running")
	}
	h.MarkDone(Outcome{TerminateBy: "done", TotalSteps: 42})
	snap := h.Snapshot()
	if snap.Status != StatusDone || snap.TotalSteps != 42 || snap.TerminateBy != "done" {
		t.Fatalf("MarkDone 后 snapshot 不对: %+v", snap)
	}
	// 再次 Mark 不该覆盖
	h.MarkFailed(errors.New("late"))
	snap2 := h.Snapshot()
	if snap2.Status != StatusDone {
		t.Fatalf("二次 Mark 不应覆盖终态: status=%s", snap2.Status)
	}
}

// TestHandle_MarkFailed 验证 failed 状态 + failureReason。
func TestHandle_MarkFailed(t *testing.T) {
	h := newHandle("tid-2", "测 upload")
	h.MarkFailed(errors.New("router fail"))
	snap := h.Snapshot()
	if snap.Status != StatusFailed {
		t.Fatalf("status=%s, want failed", snap.Status)
	}
	if snap.FailureReason != "router fail" {
		t.Fatalf("failureReason=%q, want 'router fail'", snap.FailureReason)
	}
	if h.IsRunning() {
		t.Fatalf("failed 后 IsRunning 应是 false")
	}
}

// TestRegistry_RegisterAndRunningCount 基础 case：register + RunningCount + snapshot 一致性。
func TestRegistry_RegisterAndRunningCount(t *testing.T) {
	r := NewRegistry()
	if rc := r.RunningCount(); rc != 0 {
		t.Fatalf("空 Registry RunningCount 应是 0, got %d", rc)
	}
	if (r.RunningCount() > 0) {
		t.Fatalf("空 Registry HasRunning 应是 false")
	}
	h1 := r.Register("tid-a", "brief A")
	h2 := r.Register("tid-b", "brief B")
	if rc := r.RunningCount(); rc != 2 {
		t.Fatalf("RunningCount=%d, want 2", rc)
	}
	if !(r.RunningCount() > 0) {
		t.Fatalf("有 running striker时 HasRunning 应是 true")
	}
	snaps := r.Snapshot()
	if len(snaps) != 2 || snaps[0].TaskID != h1.TaskID() || snaps[1].TaskID != h2.TaskID() {
		t.Fatalf("Snapshot 顺序错: %+v", snaps)
	}

	// h1 完成 → HasRunning 仍 true（h2 running）；RunningCount=1
	h1.MarkDone(Outcome{TerminateBy: "done", TotalSteps: 10})
	if !(r.RunningCount() > 0) {
		t.Fatalf("h1 done 但 h2 running 时 HasRunning 仍应 true")
	}
	if rc := r.RunningCount(); rc != 1 {
		t.Fatalf("h1 done 后 RunningCount=%d, want 1", rc)
	}

	// h2 完成 → HasRunning false；RunningCount=0（max_children 名额全释放，commander 可继续 spawn）
	h2.MarkFailed(errors.New("test"))
	if (r.RunningCount() > 0) {
		t.Fatalf("全部终态后 HasRunning 应 false")
	}
	if rc := r.RunningCount(); rc != 0 {
		t.Fatalf("全部终态后 RunningCount 应 0, got %d", rc)
	}
	// Snapshot 仍含 2 条历史（终态不删条目）
	if n := len(r.Snapshot()); n != 2 {
		t.Fatalf("终态不应减少 Snapshot, got %d", n)
	}

	// 再注册一个 running striker → RunningCount=1，Snapshot=3
	r.Register("tid-3", "brief3")
	if rc, n := r.RunningCount(), len(r.Snapshot()); rc != 1 || n != 3 {
		t.Fatalf("加 1 running 后 RunningCount=%d Snapshot=%d, want 1 3", rc, n)
	}
}

// TestRegistry_ConcurrentRegister 验证并发 Register 不竞态（go test -race 触发）。
func TestRegistry_ConcurrentRegister(t *testing.T) {
	r := NewRegistry()
	const n = 50
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			r.Register("tid-x", "brief")
			_ = r.RunningCount()
			_ = (r.RunningCount() > 0)
			_ = r.Snapshot()
		}(i)
	}
	wg.Wait()
	if got := len(r.Snapshot()); got != n {
		t.Fatalf("Snapshot len=%d, want %d", got, n)
	}
}

// TestRegistry_WaitAll 验证 H3 striker goroutine 清理：trackGoroutine + untrackGoroutine
// 跨多个 goroutine 后 WaitAll 阻塞直到全退；ctx 超时返 false。
func TestRegistry_WaitAll(t *testing.T) {
	// case 1: 无 goroutine 时立即返 true
	r := NewRegistry()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if !r.WaitAll(ctx) {
		t.Fatalf("空 Registry WaitAll 应立即返 true")
	}

	// case 2: 多个 goroutine 全退后 WaitAll 返 true
	r = NewRegistry()
	const n = 5
	for i := 0; i < n; i++ {
		r.trackGoroutine()
		go func() {
			time.Sleep(50 * time.Millisecond)
			r.untrackGoroutine()
		}()
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel2()
	if !r.WaitAll(ctx2) {
		t.Fatalf("goroutine 全退后 WaitAll 应返 true")
	}

	// case 3: goroutine 卡住 + ctx 超时 → WaitAll 返 false
	r = NewRegistry()
	done := make(chan struct{})
	r.trackGoroutine()
	go func() {
		<-done // 卡住等外部信号
		r.untrackGoroutine()
	}()
	ctx3, cancel3 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel3()
	if r.WaitAll(ctx3) {
		t.Fatalf("ctx 超时但 WaitAll 返 true（goroutine 没退）")
	}
	close(done) // 清理：让 goroutine 退出避免 leak
}

// TestRegistry_SnapshotIsImmutable 验证 Snapshot 返回拷贝，外部修改不影响 Registry。
func TestRegistry_SnapshotIsImmutable(t *testing.T) {
	r := NewRegistry()
	r.Register("tid-1", "brief")
	snaps := r.Snapshot()
	snaps[0].Brief = "MUTATED"
	snaps2 := r.Snapshot()
	if snaps2[0].Brief != "brief" {
		t.Fatalf("Snapshot 应是值拷贝，外部修改不应影响 Registry, got %q", snaps2[0].Brief)
	}
}
