package explorationgraph

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// 运行中引导（调整目标、指派动作、否决动作）在调用方事务内写图：调用方先锁住任务行、
// 确认任务仍在进行，再与任务行、时间线回执一同提交（见 dispatch 的引导操作）。

// ErrActionSettled 表示动作已结束（完成、失败、中止或已被否决），不能再否决。
var ErrActionSettled = errors.New("动作已结束")

// WithdrawPlannedActions 撤回规划者排好、还没开始的动作（open → aborted，记下原因），
// 返回撤回数。目标调整时调用：这些动作按旧目标排出，没有对错判断，规划者可按新目标
// 重提。用户指派的动作与执行中的动作不动（执行中的由监察者按新目标复核）。
func WithdrawPlannedActions(ctx context.Context, db Execer, taskID, reason string) (int64, error) {
	tag, err := db.Exec(ctx, `
		UPDATE exploration_node
		   SET state = 'aborted', metadata = metadata || jsonb_build_object('state_reason', $2::text)
		 WHERE task_id = $1::uuid AND kind = 'action' AND state = 'open'
		   AND metadata->>'source_type' = 'planner'`, taskID, reason)
	if err != nil {
		return 0, fmt.Errorf("撤回未开始的规划动作: %w", err)
	}
	return tag.RowsAffected(), nil
}

// AssignAction 在根目标下建一个用户指派的 open 动作（内容只有指令），返回动作 ID。
// 执行者有空位即按优先级认领；监察者不能否决它。
func AssignAction(ctx context.Context, db Querier, taskID, instruction string, priority Priority) (string, error) {
	metadata := MustJSON(map[string]string{"source_type": string(SourceUser), "priority": string(priority)})
	var id string
	err := db.QueryRow(ctx, `
		WITH root AS (
			SELECT id FROM exploration_node
			 WHERE task_id = $1::uuid AND kind = 'objective' AND coalesce(metadata->>'source_type', '') <> 'planner'
		), action AS (
			INSERT INTO exploration_node (id, task_id, kind, state, content, metadata)
			SELECT gen_random_uuid(), $1::uuid, 'action', 'open', jsonb_build_object('instruction', $2::text), $3::jsonb
			  FROM root
			RETURNING id
		), edge AS (
			INSERT INTO exploration_edge (task_id, src_id, rel, dst_id)
			SELECT $1::uuid, root.id, 'generates', action.id FROM root, action
		)
		SELECT id::text FROM action`, taskID, instruction, []byte(metadata)).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("任务 %s 没有根目标，指派的动作无处归属", taskID)
	}
	if err != nil {
		return "", fmt.Errorf("指派动作: %w", err)
	}
	return id, nil
}

// RejectActionTx 在调用方事务内否决一个待执行或执行中的动作（→ rejected），记下否决方、
// 范围与原因；与 Store.RejectAction 同一迁移，单条语句同时覆盖 open 与 running。执行中的
// 动作由执行方的取消开关感知后中断。状态前置写在被更新的行上：并发的结束先提交时，
// 语句按提交后的状态重判，不会覆盖。动作已结束返回 ErrActionSettled，不是本任务的动作
// 返回 ErrActionNotFound。
func RejectActionTx(ctx context.Context, db Querier, taskID, actionID string, by SourceType, scope RejectScope, reason string) error {
	if scope == "" {
		scope = RejectStep
	}
	var outcome string
	err := db.QueryRow(ctx, `
		WITH target AS (
			SELECT id FROM exploration_node
			 WHERE id = $2::uuid AND task_id = $1::uuid AND kind = 'action'
		), rejected AS (
			UPDATE exploration_node n
			   SET state = 'rejected',
			       metadata = n.metadata || jsonb_build_object('state_reason', $3::text, 'rejected_by', $4::text, 'rejected_scope', $5::text)
			  FROM target
			 WHERE n.id = target.id AND n.state IN ('open', 'running')
			RETURNING n.id
		)
		SELECT CASE WHEN EXISTS (SELECT 1 FROM rejected) THEN 'rejected'
		            WHEN EXISTS (SELECT 1 FROM target) THEN 'settled'
		            ELSE 'missing' END`, taskID, actionID, reason, string(by), string(scope)).Scan(&outcome)
	if err != nil {
		return fmt.Errorf("否决动作 %s: %w", actionID, err)
	}
	switch outcome {
	case "rejected":
		return nil
	case "settled":
		return fmt.Errorf("%w: %s", ErrActionSettled, actionID)
	default:
		return fmt.Errorf("%w: %s", ErrActionNotFound, actionID)
	}
}
