//go:build integration

package task

import (
	"context"
	"testing"
	"time"

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

// 暂停标记的 CAS 语义：MarkPaused/MarkResumed 只做单向迁移并报告是否发生；
// IsPaused 只对 active 任务报 true；Reopen 清残留标记；ReapStale 豁免暂停中
// 的任务（有意停摆不是孤儿）。
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
	paused, err := s.IsPaused(ctx, id)
	if err != nil || !paused {
		t.Fatalf("active 任务应报已暂停: paused=%v err=%v", paused, err)
	}
	if changed, err := s.MarkResumed(ctx, id); err != nil || !changed {
		t.Fatalf("resume 应迁移: changed=%v err=%v", changed, err)
	}
	if changed, err := s.MarkResumed(ctx, id); err != nil || changed {
		t.Fatalf("未暂停时 resume 应幂等不迁移: changed=%v err=%v", changed, err)
	}

	// 终态任务：标记不迁移、IsPaused 一律 false。
	if err := s.Abort(ctx, id, "用户停止"); err != nil {
		t.Fatalf("abort: %v", err)
	}
	if changed, err := s.MarkPaused(ctx, id); err != nil || changed {
		t.Fatalf("终态任务不得置暂停: changed=%v err=%v", changed, err)
	}
	if paused, err := s.IsPaused(ctx, id); err != nil || paused {
		t.Fatalf("终态任务 IsPaused 应为 false: paused=%v err=%v", paused, err)
	}

	// 暂停中 Reopen（FollowUp 续接）：残留标记必须清除，否则新 run 被误读为已暂停。
	id2 := newActiveTask(t, s)
	if _, err := s.MarkPaused(ctx, id2); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if err := s.Abort(ctx, id2, "暂停后停止"); err != nil {
		t.Fatalf("abort: %v", err)
	}
	if err := s.Reopen(ctx, id2); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if paused, err := s.IsPaused(ctx, id2); err != nil || paused {
		t.Fatalf("reopen 后残留暂停标记应被清除: paused=%v err=%v", paused, err)
	}
}

// ReapStale 暂停豁免：暂停中的任务心跳再旧也不判死——停摆是用户意图，
// 由继续操作复活。
func TestGuard_ReapStaleExemptsPaused(t *testing.T) {
	s := NewStore(dbtest.NewPgPool(t))
	id := newActiveTask(t, s)
	ctx := context.Background()

	if _, err := s.MarkPaused(ctx, id); err != nil {
		t.Fatalf("pause: %v", err)
	}
	// 心跳拨回 2 小时前（绕过 MarkPaused 的顺带续命），模拟长暂停。
	if _, err := s.pool.Exec(ctx,
		`UPDATE task SET heartbeat_at = now() - interval '2 hours' WHERE id=$1`, id); err != nil {
		t.Fatalf("回拨心跳: %v", err)
	}
	if ids, err := s.ReapStale(ctx, time.Minute); err != nil {
		t.Fatalf("reap: %v", err)
	} else if len(ids) != 0 {
		t.Fatalf("暂停中的任务不得被回收: %v", ids)
	}
	tk, _ := s.GetByID(ctx, id)
	if tk.Status != StatusActive || !tk.Paused {
		t.Fatalf("暂停任务应保持 active+paused: status=%s paused=%v", tk.Status, tk.Paused)
	}
}
