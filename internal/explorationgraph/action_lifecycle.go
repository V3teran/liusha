package explorationgraph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/V3teran/liusha/internal/framework/core"
)

// 动作的执行生命周期：open →(认领)→ running →(结束)→ completed/exhausted/failed，
// 或 running →(交还)→ open。
//
// 认领记下执行它的 run（metaClaimedBy），结束与交还都以「仍由我认领」为前置：同一
// 任务的新旧 run 可能短暂重叠（恢复重投、僵尸回收），新 run 回收并重新认领后，旧 run
// 迟到的结果不得改写新 run 的认领。外部决定（monitor 中止）把动作迁出
// running，执行方的结束随之落空，不会覆盖外部决定。
//
// 中断不是失败：run 被暂停、停机或超时打断时，在途动作交还 open，由下一个 run 重新
// 执行；任务进入终态时由收尾统一处置仍未结束的动作。

// metaClaimedBy 是认领者（agent_run.id）在 action metadata 中的键。
const metaClaimedBy = "claimed_by"

// ClaimedBy 返回认领该动作的 run（未被认领过为空）。
func (n *Node) ClaimedBy() string {
	if len(n.Metadata) == 0 {
		return ""
	}
	var m struct {
		ClaimedBy string `json:"claimed_by"`
	}
	if json.Unmarshal(n.Metadata, &m) != nil {
		return ""
	}
	return m.ClaimedBy
}

// ClaimAction 由 run 认领一个 open 动作（open → running）。已被认领或已结束返回 false。
func (s *Store) ClaimAction(ctx context.Context, taskID, actionID, runID string) (bool, error) {
	if runID == "" {
		return false, fmt.Errorf("认领动作 %s 需要 run ID", actionID)
	}
	return s.applyActionChange(ctx, core.GraphChangeSet{
		Transitions: []core.GraphStateTransition{{
			TaskID: taskID, NodeID: actionID, From: string(StateOpen), To: string(StateRunning),
		}},
		Annotations: []core.GraphNodeAnnotation{{
			TaskID: taskID, NodeID: actionID, Metadata: map[string]interface{}{metaClaimedBy: runID},
		}},
	})
}

// FinishAction 结束 run 认领的动作（running → completed/exhausted/failed），reason
// 非空时随状态记下原因。动作已不由该 run 认领、或已被外部迁出 running 时返回 false。
func (s *Store) FinishAction(ctx context.Context, taskID, actionID, runID string, to State, reason string) (bool, error) {
	switch to {
	case StateCompleted, StateExhausted, StateFailed:
	default:
		return false, fmt.Errorf("动作 %s 不能以 %s 结束", actionID, to)
	}
	changes := core.GraphChangeSet{
		Transitions: []core.GraphStateTransition{ownedTransition(taskID, actionID, runID, to)},
	}
	if reason != "" {
		changes.Annotations = []core.GraphNodeAnnotation{{
			TaskID: taskID, NodeID: actionID, Metadata: map[string]interface{}{"state_reason": reason},
		}}
	}
	return s.applyActionChange(ctx, changes)
}

// ReleaseAction 交还 run 认领的动作（running → open）：动作没有做完，留给下一次认领。
func (s *Store) ReleaseAction(ctx context.Context, taskID, actionID, runID string) (bool, error) {
	return s.applyActionChange(ctx, core.GraphChangeSet{
		Transitions: []core.GraphStateTransition{ownedTransition(taskID, actionID, runID, StateOpen)},
	})
}

// ReclaimRunningActions 把任务中仍为 running 的动作交还 open，返回交还数。新 run 开始
// 时调用：它此刻独占该任务，任何 running 都是之前的 run 留下的（崩溃或未交还）。
func (s *Store) ReclaimRunningActions(ctx context.Context, taskID string) (int, error) {
	running, err := s.ListActionsByState(ctx, taskID, StateRunning)
	if err != nil {
		return 0, fmt.Errorf("读取在途动作: %w", err)
	}
	reclaimed := 0
	for _, a := range running {
		ok, err := s.applyActionChange(ctx, core.GraphChangeSet{
			Transitions: []core.GraphStateTransition{{
				TaskID: taskID, NodeID: a.ID, From: string(StateRunning), To: string(StateOpen),
			}},
		})
		if err != nil {
			return reclaimed, fmt.Errorf("交还动作 %s: %w", a.ID, err)
		}
		if ok {
			reclaimed++
		}
	}
	return reclaimed, nil
}

func ownedTransition(taskID, actionID, runID string, to State) core.GraphStateTransition {
	return core.GraphStateTransition{
		TaskID: taskID, NodeID: actionID, From: string(StateRunning), To: string(to),
		Guard: map[string]string{metaClaimedBy: runID},
	}
}

// applyActionChange 应用一次动作迁移：前置不成立（已被并发迁移或不属于本 run）
// 返回 false 而非错误。
func (s *Store) applyActionChange(ctx context.Context, changes core.GraphChangeSet) (bool, error) {
	err := s.graphStore.ApplyChangeSet(ctx, changes)
	if errors.Is(err, core.ErrGraphStateConflict) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
