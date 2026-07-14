package task

import (
	"context"
	"fmt"
	"time"
)

// Abort 把 task 推进到 aborted 终态：写 status / ended_at / error_message。
// 用户主动停 / ctx 取消 / 错误路径共用。
func (s *Store) Abort(ctx context.Context, id, errMsg string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE task SET
			status='aborted',
			ended_at=now(),
			error_message=$1
		WHERE id=$2`, errMsg, id)
	if err != nil {
		return fmt.Errorf("abort task %s: %w", id, err)
	}
	return nil
}

// Complete 把 task 置为 completed（orchestrator run 自然跑完的成功终态），写 ended_at。
// 与 Abort 区别：completed 无 error_message（成功收尾），aborted 带原因。
func (s *Store) Complete(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE task SET
			status='completed',
			ended_at=now()
		WHERE id=$1`, id)
	if err != nil {
		return fmt.Errorf("complete task %s: %w", id, err)
	}
	return nil
}

// Reopen 把已终态的 task 置回 active，清 ended_at/error_message（仅 active 模式的 FollowUp 续接）。
//
// 累计停顿：复活前把「上次 ended_at → 现在」的用户停顿累加进 paused_ms（PG UPDATE 表达式用行旧值），
// WallclockMs 据此扣除停顿 = 纯 agent 工作耗时。ended_at 为空则不累加（COALESCE 兜底 0）。
func (s *Store) Reopen(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE task SET
			status='active',
			paused_ms = paused_ms + COALESCE((EXTRACT(EPOCH FROM (now() - ended_at)) * 1000)::bigint, 0),
			ended_at=NULL,
			error_message=''
		WHERE id=$1`, id)
	if err != nil {
		return fmt.Errorf("reopen task %s: %w", id, err)
	}
	return nil
}

// SetTargetHost 回填 target_host（scanner 从 brief 抽到真实 host 时）。幂等覆盖，best-effort。
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
// active / passive 判死阈值不同：active run 内可能跑长工具（sqlmap）+ 慢 LLM，阈值较长；
// passive 单批分析轻量，阈值较短。caller 按 mode 传不同 staleAfter，各调用一次。返回回收条数。
func (s *Store) ReapStale(ctx context.Context, mode Mode, staleAfter time.Duration) (int, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE task SET
			status='aborted',
			ended_at=now(),
			error_message='心跳超时（scanner 进程崩溃或任务卡死）'
		WHERE status='active' AND mode=$1 AND heartbeat_at < now() - $2::interval`,
		string(mode), fmt.Sprintf("%d milliseconds", staleAfter.Milliseconds()))
	if err != nil {
		return 0, fmt.Errorf("reap stale %s tasks: %w", mode, err)
	}
	return int(tag.RowsAffected()), nil
}
