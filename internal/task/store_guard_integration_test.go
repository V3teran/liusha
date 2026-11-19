//go:build integration

package task

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/V3teran/liusha/internal/dbtest"
)

// newActiveTask 建一个 active task（迁移测试起点）。
func newActiveTask(t *testing.T, s *Store) string {
	t.Helper()
	tk, err := s.Create(context.Background(), NewParams{
		AssignmentID: dbtest.SeedAssignment(t, s.pool),
		Goal:         "guard test",
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
	if _, err := s.Abort(context.Background(), id, "迟到的停止"); err == nil {
		t.Fatal("completed 后 Abort 应被拒（不得覆盖成功终态）")
	}
	tk, _ := s.GetByID(context.Background(), id)
	if tk.Status != StatusCompleted {
		t.Fatalf("终态被覆盖: %s", tk.Status)
	}
}

// 迁移守卫：aborted 后迟到的 Complete 不得覆盖停止判定（典型时序：用户点停止 →
// api 先 abort → runner 循环随后自然收敛）。
func TestGuard_AbortThenCompleteRejected(t *testing.T) {
	s := NewStore(dbtest.NewPgPool(t))
	id := newActiveTask(t, s)

	if _, err := s.Abort(context.Background(), id, "用户停止"); err != nil {
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

// 幂等：同终态重复迁移返回 nil（并发双写者先到后到不互斥），只有真正迁移的一方 applied——
// 时间线终态节点据此只落一次。
func TestGuard_IdempotentSameTerminal(t *testing.T) {
	s := NewStore(dbtest.NewPgPool(t))
	id := newActiveTask(t, s)

	if applied, err := s.Abort(context.Background(), id, "用户停止"); err != nil || !applied {
		t.Fatalf("abort 1: applied=%v err=%v", applied, err)
	}
	if applied, err := s.Abort(context.Background(), id, "恢复监控判死"); err != nil || applied {
		t.Fatalf("abort 2 应幂等且不算迁移: applied=%v err=%v", applied, err)
	}
	if err := s.Complete(context.Background(), id); err == nil {
		t.Fatal("aborted 后 Complete 仍应被拒")
	}
}

// ReopenTx 是唯一的终态出口：aborted → active 后可再次 Complete。
func TestGuard_ReopenReenablesTransitions(t *testing.T) {
	s := NewStore(dbtest.NewPgPool(t))
	id := newActiveTask(t, s)

	if _, err := s.Abort(context.Background(), id, "用户停止"); err != nil {
		t.Fatalf("abort: %v", err)
	}
	if err := reopen(s, context.Background(), id); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if err := s.Complete(context.Background(), id); err != nil {
		t.Fatalf("reopen 后 complete 应放行: %v", err)
	}
}

// 暂停标记的 CAS 语义：MarkPaused/MarkResumed 只做单向迁移并报告是否发生；
// ReopenTx 清残留标记。
func TestGuard_PauseMarkLifecycle(t *testing.T) {
	s := NewStore(dbtest.NewPgPool(t))
	id := newActiveTask(t, s)
	ctx := context.Background()

	if changed, err := s.MarkPaused(ctx, id); err != nil || !changed {
		t.Fatalf("首次 pause 应迁移: changed=%v err=%v", changed, err)
	}
	if changed, err := s.MarkPaused(ctx, id); err != nil || changed {
		t.Fatalf("重复 pause 应幂等不迁移: changed=%v err=%v", changed, err)
	}
	if tk, err := s.GetByID(ctx, id); err != nil || tk.Status != StatusActive || !tk.Paused {
		t.Fatalf("active 任务应读回 paused=true: status=%s paused=%v err=%v", tk.Status, tk.Paused, err)
	}
	if changed, err := s.MarkResumed(ctx, id); err != nil || !changed {
		t.Fatalf("resume 应迁移: changed=%v err=%v", changed, err)
	}
	if changed, err := s.MarkResumed(ctx, id); err != nil || changed {
		t.Fatalf("未暂停时 resume 应幂等不迁移: changed=%v err=%v", changed, err)
	}

	// 终态任务：标记不迁移（循环按行观察，终态即退出，不看 paused）。
	if _, err := s.Abort(ctx, id, "用户停止"); err != nil {
		t.Fatalf("abort: %v", err)
	}
	if changed, err := s.MarkPaused(ctx, id); err != nil || changed {
		t.Fatalf("终态任务不得置暂停: changed=%v err=%v", changed, err)
	}

	// 暂停中被停止的任务重开（对话里续接）：残留标记必须清除，否则新 run 被误读为已暂停。
	id2 := newActiveTask(t, s)
	if _, err := s.MarkPaused(ctx, id2); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if _, err := s.Abort(ctx, id2, "暂停后停止"); err != nil {
		t.Fatalf("abort: %v", err)
	}
	if err := reopen(s, ctx, id2); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if tk, err := s.GetByID(ctx, id2); err != nil || tk.Paused {
		t.Fatalf("reopen 后残留暂停标记应被清除: paused=%v err=%v", tk.Paused, err)
	}
}

// reopen 在独立事务内重开任务（生产路径由对话续接在引导事务里调用 ReopenTx）。
func reopen(s *Store, ctx context.Context, id string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error { return ReopenTx(ctx, tx, id) })
}
