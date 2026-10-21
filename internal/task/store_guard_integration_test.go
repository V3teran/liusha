//go:build integration

package task

import (
	"context"
	"testing"

	"github.com/V3teran/liusha/internal/dbtest"
)

// newActiveTask 建一个 active task（迁移测试起点）。
func newActiveTask(t *testing.T, s *Store) string {
	t.Helper()
	tk, err := s.Create(context.Background(), NewParams{
		AssignmentID: dbtest.SeedAssignment(t, s.pool),
		Brief:        "guard test",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	return tk.ID
}

// 迁移守卫：completed 后迟到的 Abort 不得把成功终态改写成 aborted（用户停止 vs
// runner 收敛的竞态——先到者赢，后到者被拒且不覆盖）。
func TestGuard_CompleteThenAbortRejected(t *testing.T) {
	s := NewStore(dbtest.NewPgPool(t))
	id := newActiveTask(t, s)

	if err := s.Complete(context.Background(), id); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if err := s.Abort(context.Background(), id, "迟到的停止"); err == nil {
		t.Fatal("completed 后 Abort 应被拒（不得覆盖成功终态）")
	}
	tk, _ := s.GetByID(context.Background(), id)
	if tk.Status != StatusCompleted {
		t.Fatalf("终态被覆盖: %s", tk.Status)
	}
}

// 迁移守卫：aborted 后迟到的 Complete 不得覆盖停止裁决（典型时序：用户点停止 →
// api 先 abort → runner 循环随后自然收敛）。
func TestGuard_AbortThenCompleteRejected(t *testing.T) {
	s := NewStore(dbtest.NewPgPool(t))
	id := newActiveTask(t, s)

	if err := s.Abort(context.Background(), id, "用户停止"); err != nil {
		t.Fatalf("abort: %v", err)
	}
	if err := s.Complete(context.Background(), id); err == nil {
		t.Fatal("aborted 后 Complete 应被拒（不得覆盖停止终态）")
	}
	tk, _ := s.GetByID(context.Background(), id)
	if tk.Status != StatusAborted {
		t.Fatalf("终态被覆盖: %s", tk.Status)
	}
}

// 幂等：同终态重复迁移返回 nil（并发双写者先到后到不互斥）。
func TestGuard_IdempotentSameTerminal(t *testing.T) {
	s := NewStore(dbtest.NewPgPool(t))
	id := newActiveTask(t, s)

	if err := s.Abort(context.Background(), id, "用户停止"); err != nil {
		t.Fatalf("abort 1: %v", err)
	}
	if err := s.Abort(context.Background(), id, "reaper 判死"); err != nil {
		t.Fatalf("abort 2（应幂等）: %v", err)
	}
	if err := s.Complete(context.Background(), id); err == nil {
		t.Fatal("aborted 后 Complete 仍应被拒")
	}
}

// Reopen 是唯一的终态出口：aborted → active 后可再次 Complete。
func TestGuard_ReopenReenablesTransitions(t *testing.T) {
	s := NewStore(dbtest.NewPgPool(t))
	id := newActiveTask(t, s)

	if err := s.Abort(context.Background(), id, "用户停止"); err != nil {
		t.Fatalf("abort: %v", err)
	}
	if err := s.Reopen(context.Background(), id); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if err := s.Complete(context.Background(), id); err != nil {
		t.Fatalf("reopen 后 complete 应放行: %v", err)
	}
}
