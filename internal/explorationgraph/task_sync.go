package explorationgraph

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// TaskStatus 是图所属任务的生命周期状态，图上的开放工作随它收口。
type TaskStatus string

// 任务生命周期状态（与任务表的 status 取值一致）。
const (
	TaskActive    TaskStatus = "active"
	TaskCompleted TaskStatus = "completed"
	TaskAborted   TaskStatus = "aborted"
)

// Execer 是同步所需的最小写能力（pgx.Tx / pgxpool.Pool 都满足）。
type Execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Querier 是只读判定所需的最小查询能力（pgx.Tx / pgxpool.Pool 都满足）。
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// ConvergenceHolds 判定一次收敛的依据此刻是否仍然成立：规划者宣告收敛时依据的目标
// 未被改写，且图上没有待执行或执行中的动作（收敛之后被指派的动作）。任务落 completed
// 的事务在锁住任务行之后调用它：引导写入同样先锁任务行，两者串行，判定不会过期。
func ConvergenceHolds(ctx context.Context, db Querier, taskID string, basis GoalBasis) (bool, error) {
	var holds bool
	err := db.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM exploration_node WHERE id = $2 AND task_id = $1 AND version = $3)
		   AND NOT EXISTS (SELECT 1 FROM exploration_node
		                    WHERE task_id = $1 AND kind = 'action' AND state IN ('open', 'running'))`,
		taskID, basis.ObjectiveID, basis.Version).Scan(&holds)
	if err != nil {
		return false, fmt.Errorf("判定收敛依据: %w", err)
	}
	return holds, nil
}

// SyncWithTask 让任务图跟随任务状态，必须与任务状态迁移在同一事务内执行——任务
// 结局与图上的收口要么同时成立、要么都不成立，任务进入终态的每条路径（runner 收尾、
// 用户停止、恢复上限）因此都不会留下仍 open 的工作。
//
//   - 根目标代表任务本身，状态跟随任务：active → open，completed → completed，
//     aborted → aborted；
//   - 任务结束时仍 open 的派生目标：正常完成是未达成的方向，落 exhausted；被终止
//     则落 aborted；
//   - 任务结束时未结束的动作（open / running）落 aborted 并记下原因；仍在执行的旧
//     run 迟到的结果因动作已不在 running 而落空；
//   - 已收口的派生目标与动作是历史，任务重开时不动；待验证的假设如实保持 pending。
func SyncWithTask(ctx context.Context, db Execer, taskIDs []string, status TaskStatus) error {
	if len(taskIDs) == 0 {
		return nil
	}
	switch status {
	case TaskActive:
		_, err := db.Exec(ctx, `
			UPDATE exploration_node SET state = 'open'
			 WHERE task_id = ANY($1::uuid[]) AND kind = 'objective'
			   AND coalesce(metadata->>'source_type', '') <> 'planner'
			   AND state IN ('completed', 'aborted', 'failed')`, taskIDs)
		if err != nil {
			return fmt.Errorf("重开根目标: %w", err)
		}
		return nil
	case TaskCompleted, TaskAborted:
	default:
		return fmt.Errorf("未知任务状态 %q", status)
	}

	derived, reason := "aborted", "任务已终止"
	if status == TaskCompleted {
		derived, reason = "exhausted", "任务已完成"
	}
	statements := []struct {
		what string
		sql  string
		args []any
	}{
		{"根目标", `
			UPDATE exploration_node SET state = $2
			 WHERE task_id = ANY($1::uuid[]) AND kind = 'objective'
			   AND coalesce(metadata->>'source_type', '') <> 'planner' AND state = 'open'`,
			[]any{taskIDs, string(status)}},
		{"派生目标", `
			UPDATE exploration_node SET state = $2
			 WHERE task_id = ANY($1::uuid[]) AND kind = 'objective'
			   AND metadata->>'source_type' = 'planner' AND state = 'open'`,
			[]any{taskIDs, derived}},
		{"动作", `
			UPDATE exploration_node
			   SET state = 'aborted', metadata = metadata || jsonb_build_object('state_reason', $2::text)
			 WHERE task_id = ANY($1::uuid[]) AND kind = 'action' AND state IN ('open', 'running')`,
			[]any{taskIDs, reason}},
	}
	for _, st := range statements {
		if _, err := db.Exec(ctx, st.sql, st.args...); err != nil {
			return fmt.Errorf("收口%s: %w", st.what, err)
		}
	}
	return nil
}
