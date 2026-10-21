package task

import (
	"context"
	"fmt"
	"time"
)

// errConflict 复用于各迁移的冲突返回（调用方只关心「未迁移」，不区分类别）。
var errConflict = fmt.Errorf("task 状态迁移冲突")

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
	return fmt.Errorf("%w: task %s 当前 %s，拒绝迁入 %s", errConflict, id, t.Status, want)
}

// Reopen 把已终态的 task 置回 active，清 ended_at/error_message（仅 active 模式的 FollowUp 续接）。
//
// 累计停顿：复活前把「上次 ended_at → 现在」的用户停顿累加进 paused_ms（PG UPDATE 表达式用行旧值），
// WallclockMs 据此扣除停顿 = 纯 agent 工作耗时。ended_at 为空则不累加（COALESCE 兜底 0）。
// heartbeat_at 一并重置为 now：复活即续命——否则旧心跳残留，队列积压场景下 reaper
// 会把刚 Reopen 的任务按超时判死（Reopen → runner 领取之间存在无心跳空窗）。
func (s *Store) Reopen(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE task SET
			status='active',
			paused_ms = paused_ms + COALESCE((EXTRACT(EPOCH FROM (now() - ended_at)) * 1000)::bigint, 0),
			heartbeat_at = now(),
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
//
// 跨场景统一巡检：一次扫全部 active task，按 staleAfter 判死。返回回收的 task ID
// 列表（调用方据此做后续可见性动作——时间线节点/告警）。
func (s *Store) ReapStale(ctx context.Context, staleAfter time.Duration) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		UPDATE task SET
			status='aborted',
			ended_at=now(),
			error_message='心跳超时（runner 进程崩溃或任务卡死）'
		WHERE status='active' AND heartbeat_at < now() - $1::interval
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
