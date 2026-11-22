package task

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/V3teran/liusha/internal/explorationgraph"
)

// ErrConflict 是终态迁移被拒（另一终态已先落，不覆盖）的哨兵；API 层据此映射 409。
var ErrConflict = fmt.Errorf("task 状态迁移冲突")

// ErrStaleConvergence 是收敛依据已失效的哨兵：规划者宣告收敛之后目标被改写、或图上
// 又有了待执行的动作，任务不能按那次收敛结束。
var ErrStaleConvergence = fmt.Errorf("收敛依据已失效：目标已调整或有了新的待执行动作")

// ErrNotActive 是「操作仅对进行中任务有效」的哨兵（暂停/继续打到终态任务）；
// API 层据此映射 409。
var ErrNotActive = fmt.Errorf("任务已结束，仅进行中的任务可执行该操作")

// Abort 把 task 推进到 aborted 终态：写 status / ended_at / abort_reason。
// 用户主动停 / ctx 取消 / 错误路径共用。
//
// 守卫：仅 active 可迁入 aborted（CAS）；已 aborted 幂等（并发双写者——用户停止 vs
// 恢复监控判死——先到者赢）；已 completed 是冲突（迟到的 abort 不得把成功终态改写成
// aborted），返回错误但不覆盖。applied 报告本次是否真正完成了迁移：时间线的终态节点只由
// 真正迁移的一方落，幂等的重复停止不重复落节点。
func (s *Store) Abort(ctx context.Context, id, reason string) (applied bool, err error) {
	return s.finishApplied(ctx, id, StatusAborted, nil, `
		UPDATE task SET
			status='aborted',
			ended_at=now(),
			abort_reason=$2
		WHERE id=$1 AND status='active'`, reason)
}

// Complete 把 task 置为 completed（planner run 自然跑完的成功终态），写 ended_at。
//
// 守卫：仅 active 可迁入 completed（CAS）；已 completed 幂等返回 nil；已 aborted
// 是冲突（迟到的成功收尾不得覆盖用户停止/判死判定），返回错误但不覆盖——
// 典型时序：用户点停止（api 先 abort）→ runner 循环随后自然收敛，此处被拒。
func (s *Store) Complete(ctx context.Context, id string) error {
	_, err := s.finishApplied(ctx, id, StatusCompleted, nil, completeSQL)
	return err
}

// CompleteConverged 把因规划收敛而结束的 task 置为 completed，前置是收敛依据仍成立
// （见 explorationgraph.ConvergenceHolds）：判定在锁住任务行之后进行，与目标调整、指派
// 动作这些先锁任务行的写入串行。依据失效返回 ErrStaleConvergence，任务保持 active、
// 图不收口；其余守卫与 Complete 相同。
func (s *Store) CompleteConverged(ctx context.Context, id string, basis explorationgraph.GoalBasis) error {
	_, err := s.finishApplied(ctx, id, StatusCompleted, func(tx pgx.Tx) error {
		holds, err := explorationgraph.ConvergenceHolds(ctx, tx, id, basis)
		if err != nil {
			return err
		}
		if !holds {
			return ErrStaleConvergence
		}
		return nil
	}, completeSQL)
	return err
}

const completeSQL = `
		UPDATE task SET
			status='completed',
			ended_at=now()
		WHERE id=$1 AND status='active'`

// finishApplied 执行一次 active → 终态的 CAS 迁移，并在同一事务内让任务图收口（见
// explorationgraph.SyncWithTask）：任务结局与图上的收口同时成立。guard 非空时先锁住
// 任务行（仍 active 才判定），guard 返回错误即整体放弃。CAS 未命中时不改动任何东西，
// 交 terminalOrConflict 判定幂等或冲突。
// 返回本次是否真正完成了迁移（已是目标终态的幂等成功为 false）。
func (s *Store) finishApplied(ctx context.Context, id string, to Status, guard func(pgx.Tx) error, update string, args ...any) (bool, error) {
	applied := false
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if guard != nil {
			var active bool
			if err := tx.QueryRow(ctx, `SELECT status = 'active' FROM task WHERE id = $1 FOR UPDATE`, id).Scan(&active); err != nil {
				return fmt.Errorf("锁定任务: %w", err)
			}
			if !active {
				return nil
			}
			if err := guard(tx); err != nil {
				return err
			}
		}
		tag, err := tx.Exec(ctx, update, append([]any{id}, args...)...)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		applied = true
		return explorationgraph.SyncWithTask(ctx, tx, []string{id}, graphStatus(to))
	})
	if err != nil {
		return false, fmt.Errorf("task %s 迁入 %s: %w", id, to, err)
	}
	if !applied {
		return false, s.terminalOrConflict(ctx, id, to)
	}
	return true, nil
}

// graphStatus 把任务状态映射为任务图的生命周期状态。
func graphStatus(st Status) explorationgraph.TaskStatus {
	switch st {
	case StatusCompleted:
		return explorationgraph.TaskCompleted
	case StatusAborted:
		return explorationgraph.TaskAborted
	default:
		return explorationgraph.TaskActive
	}
}

// terminalOrConflict 是 CAS 零行更新后的判定：当前已是目标终态 → 幂等成功；
// 另一终态 → 冲突错误（不覆盖）；active/不存在 → 状态竞争窗口（视为冲突，调用方重试语义自理）。
func (s *Store) terminalOrConflict(ctx context.Context, id string, want Status) error {
	t, err := s.GetByID(ctx, id)
	if err != nil {
		return fmt.Errorf("task %s 迁移后读回失败: %w", id, err)
	}
	if t.Status == want {
		return nil
	}
	return fmt.Errorf("%w: task %s 当前 %s，拒绝迁入 %s", ErrConflict, id, t.Status, want)
}

// ReopenTx 在调用方事务内把已终态的 task 置回 active（对话里续接已结束的任务），清
// ended_at/abort_reason/paused。paused 必须一并清：上轮生命周期结束时若停在暂停中，
// 残留标记会让新跑的循环被误读为「已暂停」。
//
// 累计停顿：复活前把「上次 ended_at → 现在」的用户停顿累加进 paused_ms（PG UPDATE 表达式用行旧值），
// Task.Elapsed 据此扣除停顿。ended_at 为空则不累加（COALESCE 兜底 0）。
func ReopenTx(ctx context.Context, tx pgx.Tx, id string) error {
	if _, err := tx.Exec(ctx, `
		UPDATE task SET
			status='active',
			paused=false,
			paused_ms = paused_ms + COALESCE((EXTRACT(EPOCH FROM (now() - ended_at)) * 1000)::bigint, 0),
			ended_at=NULL,
			abort_reason=''
		WHERE id=$1`, id); err != nil {
		return fmt.Errorf("reopen task %s: %w", id, err)
	}
	// 根目标代表任务本身，随任务回到 open。
	return explorationgraph.SyncWithTask(ctx, tx, []string{id}, explorationgraph.TaskActive)
}

// MarkPaused 把 active 任务置为暂停中（CAS：已暂停的行不重复迁移）。返回是否
// 发生了迁移——false 即已是暂停态，调用方据此跳过重复的时间线/通知动作。
func (s *Store) MarkPaused(ctx context.Context, id string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE task SET paused = true
		WHERE id = $1 AND status = 'active' AND NOT paused`, id)
	if err != nil {
		return false, fmt.Errorf("mark paused: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// MarkResumed 解除暂停（CAS：仅暂停中的行）。返回是否发生迁移——false 即
// 未在暂停（或已终态），调用方视为幂等 no-op。
func (s *Store) MarkResumed(ctx context.Context, id string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE task SET paused = false
		WHERE id = $1 AND status = 'active' AND paused`, id)
	if err != nil {
		return false, fmt.Errorf("mark resumed: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}
