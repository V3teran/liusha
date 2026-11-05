package task

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrConflict 是终态迁移被拒（另一终态已先落，不覆盖）的哨兵；API 层据此映射 409。
var ErrConflict = fmt.Errorf("task 状态迁移冲突")

// ErrNotActive 是「操作仅对进行中任务有效」的哨兵（暂停/继续打到终态任务）；
// API 层据此映射 409。
var ErrNotActive = fmt.Errorf("任务已结束，仅进行中的任务可执行该操作")

// Abort 把 task 推进到 aborted 终态：写 status / ended_at / error_message。
// 用户主动停 / ctx 取消 / 错误路径共用。
//
// 守卫：仅 active 可迁入 aborted（CAS）；已 aborted 幂等返回 nil（并发双写者——
// 用户停止 vs reaper 判死——先到者赢）；已 completed 是冲突（迟到的 abort 不得
// 把成功终态改写成 aborted），返回错误但不覆盖。
func (s *Store) Abort(ctx context.Context, id, errMsg string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE task SET
			status='aborted',
			ended_at=now(),
			error_message=$1
		WHERE id=$2 AND status='active'`, errMsg, id)
	if err != nil {
		return fmt.Errorf("abort task %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return s.terminalOrConflict(ctx, id, StatusAborted)
	}
	return nil
}

// Complete 把 task 置为 completed（planner run 自然跑完的成功终态），写 ended_at。
//
// 守卫：仅 active 可迁入 completed（CAS）；已 completed 幂等返回 nil；已 aborted
// 是冲突（迟到的成功收尾不得覆盖用户停止/判死裁决），返回错误但不覆盖——
// 典型时序：用户点停止（api 先 abort）→ runner 循环随后自然收敛，此处被拒。
func (s *Store) Complete(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE task SET
			status='completed',
			ended_at=now()
		WHERE id=$1 AND status='active'`, id)
	if err != nil {
		return fmt.Errorf("complete task %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return s.terminalOrConflict(ctx, id, StatusCompleted)
	}
	return nil
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

// Reopen 把已终态的 task 置回 active，清 ended_at/error_message/paused（仅 active
// 模式的 FollowUp 续接）。paused 必须一并清：上轮生命周期结束时若停在暂停中，
// 残留标记会让新跑的循环被误读为「已暂停」。
//
// 累计停顿：复活前把「上次 ended_at → 现在」的用户停顿累加进 paused_ms（PG UPDATE 表达式用行旧值），
// WallclockMs 据此扣除停顿 = 纯 agent 工作耗时。ended_at 为空则不累加（COALESCE 兜底 0）。
// heartbeat_at 一并重置为 now：复活即续命——否则旧心跳残留，队列积压场景下 reaper
// 会把刚 Reopen 的任务按超时判死（Reopen → runner 领取之间存在无心跳空窗）。
func (s *Store) Reopen(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE task SET
			status='active',
			paused=false,
			paused_ms = paused_ms + COALESCE((EXTRACT(EPOCH FROM (now() - ended_at)) * 1000)::bigint, 0),
			heartbeat_at=now(),
			ended_at=NULL,
			error_message=''
		WHERE id=$1`, id)
	if err != nil {
		return fmt.Errorf("reopen task %s: %w", id, err)
	}
	return nil
}

// SetTargetHost 回填 target_host（runner 从 brief 抽到真实 host 时）。幂等覆盖，best-effort。
func (s *Store) SetTargetHost(ctx context.Context, id, host string) error {
	_, err := s.pool.Exec(ctx, `UPDATE task SET target_host=$1 WHERE id=$2`, host, id)
	if err != nil {
		return fmt.Errorf("set target_host %s: %w", id, err)
	}
	return nil
}

// Heartbeat 刷新 active task 的 heartbeat_at（B2 探活：agent 每次工具调用驱动，节流见 caller）。
// 仅对 active 行生效——终态行不刷（避免复活已结束的扫描）。best-effort，不阻塞业务。
func (s *Store) Heartbeat(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE task SET heartbeat_at=now() WHERE id=$1 AND status='active'`, id)
	if err != nil {
		return fmt.Errorf("heartbeat task %s: %w", id, err)
	}
	return nil
}

// ReapStale 把心跳超时的 active task 判为 aborted（进程崩溃/卡死的孤儿）。
// 暂停中的任务豁免：暂停即有意停摆（无 run 在跑、无心跳是常态），由继续操作
// 复活，判死条件永不成立才对。
//
// 跨场景统一巡检：一次扫全部 active task，按 staleAfter 判死。返回回收的 task ID
// 列表（调用方据此做后续可见性动作——时间线节点/告警）。
func (s *Store) ReapStale(ctx context.Context, staleAfter time.Duration) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		UPDATE task SET
			status='aborted',
			ended_at=now(),
			error_message='心跳超时（runner 进程崩溃或任务卡死）'
		WHERE status='active' AND NOT paused AND heartbeat_at < now() - $1::interval
		RETURNING id`,
		fmt.Sprintf("%d milliseconds", staleAfter.Milliseconds()))
	if err != nil {
		return nil, fmt.Errorf("reap stale tasks: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return ids, fmt.Errorf("scan reaped task id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// MarkPaused 把 active 任务置为暂停中（CAS：已暂停的行不重复迁移）。返回是否
// 发生了迁移——false 即已是暂停态，调用方据此跳过重复的时间线/通知动作。
// heartbeat_at 顺带续命：暂停期无 run 在跑，心跳就此定格，由 ReapStale 的
// 暂停豁免兜住；续命是为了拉平「暂停落库 → 循环退出（≤5s）」窗口内的判死风险。
func (s *Store) MarkPaused(ctx context.Context, id string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE task SET paused = true, heartbeat_at = now()
		WHERE id = $1 AND status = 'active' AND NOT paused`, id)
	if err != nil {
		return false, fmt.Errorf("mark paused: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// MarkResumed 解除暂停（CAS：仅暂停中的行）。返回是否发生迁移——false 即
// 未在暂停（或已终态），调用方视为幂等 no-op。心跳同步续命，拉平
// 「解除暂停 → 恢复 run 领取」窗口。
func (s *Store) MarkResumed(ctx context.Context, id string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE task SET paused = false, heartbeat_at = now()
		WHERE id = $1 AND status = 'active' AND paused`, id)
	if err != nil {
		return false, fmt.Errorf("mark resumed: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// IsPaused 读任务是否处于「active 且暂停中」。终态/不存在一律 false——
// 暂停只对进行中任务有意义，调用方（认知循环的暂停观察）无需再判状态。
func (s *Store) IsPaused(ctx context.Context, id string) (bool, error) {
	var paused bool
	err := s.pool.QueryRow(ctx,
		`SELECT paused FROM task WHERE id = $1 AND status = 'active'`, id).
		Scan(&paused)
	if err == pgx.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("is paused: %w", err)
	}
	return paused, nil
}
