package subtask

import (
	"errors"
	"sync"
	"testing"
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

// TestRegistry_RegisterAndCount 基础 case：register + count + snapshot 一致性。
func TestRegistry_RegisterAndCount(t *testing.T) {
	r := NewRegistry()
	if r.Count() != 0 {
		t.Fatalf("空 Registry Count 应是 0")
	}
	if r.HasRunning() {
		t.Fatalf("空 Registry HasRunning 应是 false")
	}
	h1 := r.Register("tid-a", "brief A")
	h2 := r.Register("tid-b", "brief B")
	if r.Count() != 2 {
		t.Fatalf("Count=%d, want 2", r.Count())
	}
	if !r.HasRunning() {
		t.Fatalf("有 running 子时 HasRunning 应是 true")
	}
	snaps := r.Snapshot()
	if len(snaps) != 2 || snaps[0].TaskID != h1.TaskID() || snaps[1].TaskID != h2.TaskID() {
		t.Fatalf("Snapshot 顺序错: %+v", snaps)
	}

	// h1 完成 → HasRunning 仍 true（h2 running）
	h1.MarkDone(Outcome{TerminateBy: "done", TotalSteps: 10})
	if !r.HasRunning() {
		t.Fatalf("h1 done 但 h2 running 时 HasRunning 仍应 true")
	}

	// h2 完成 → HasRunning false
	h2.MarkFailed(errors.New("test"))
	if r.HasRunning() {
		t.Fatalf("全部终态后 HasRunning 应 false")
	}
	// Count 不变（含终态）
	if r.Count() != 2 {
		t.Fatalf("终态不应减少 Count, got %d", r.Count())
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
			_ = r.Count()
			_ = r.HasRunning()
			_ = r.Snapshot()
		}(i)
	}
	wg.Wait()
	if r.Count() != n {
		t.Fatalf("Count=%d, want %d", r.Count(), n)
	}
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
