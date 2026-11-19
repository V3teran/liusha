package explorationgraph

import (
	"context"
	"errors"
	"fmt"

	"github.com/V3teran/liusha/internal/framework/core"
)

// stopRetries 是停止探索在动作状态并发变化时重新取快照的次数上限。冲突只发生在快照
// 与提交之间有动作被认领或结束——窗口是毫秒级，几次重取足以收敛。
const stopRetries = 5

// StopExploration 落定规划者「停止探索」的结论：收回规划者排出、还没做完的动作（待执行
// 与执行中 → 已中止，记下原因），返回被收回的动作。用户指派的动作不动，照常做完。
//
// 结论以 basis（规划所依据的目标）为前置：规划期间目标被改写返回 ErrGoalChanged，
// 什么都不收回。执行中的动作由执行方的持有侦测感知后中断，其迟到的结束因认领前置不
// 成立而落空。收回按快照逐个 CAS：快照之后有动作被认领或结束时整批重来，不会把已
// 结束的动作改回中止。
func (s *Store) StopExploration(ctx context.Context, taskID string, basis GoalBasis, reason string) ([]string, error) {
	if basis.ObjectiveID == "" {
		return nil, fmt.Errorf("停止探索缺少依据（任务 %s）", taskID)
	}
	for attempt := 0; attempt < stopRetries; attempt++ {
		withdrawn, changes, err := s.stopChanges(ctx, taskID, basis, reason)
		if err != nil {
			return nil, err
		}
		err = s.graphStore.ApplyChangeSet(ctx, changes)
		switch {
		case err == nil:
			return withdrawn, nil
		case errors.Is(err, core.ErrVersionMismatch):
			return nil, ErrGoalChanged
		case errors.Is(err, core.ErrGraphStateConflict):
			continue
		default:
			return nil, fmt.Errorf("停止探索: %w", err)
		}
	}
	return nil, fmt.Errorf("停止探索: 动作状态持续变化，%d 次快照均未能提交: %w", stopRetries, core.ErrGraphStateConflict)
}

// stopChanges 基于当前快照构造收回变更集：规划者的待执行与执行中动作 → 已中止。
func (s *Store) stopChanges(ctx context.Context, taskID string, basis GoalBasis, reason string) ([]string, core.GraphChangeSet, error) {
	changes := core.GraphChangeSet{
		Preconditions: []core.GraphNodeVersion{{TaskID: taskID, NodeID: basis.ObjectiveID, Version: basis.Version}},
	}
	var withdrawn []string
	for _, from := range []State{StateOpen, StateRunning} {
		actions, err := s.ListActionsByState(ctx, taskID, from)
		if err != nil {
			return nil, changes, fmt.Errorf("读取%s动作: %w", from, err)
		}
		for _, a := range actions {
			if a.SourceType != SourcePlanner {
				continue
			}
			withdrawn = append(withdrawn, a.ID)
			changes.Transitions = append(changes.Transitions, core.GraphStateTransition{
				TaskID: taskID, NodeID: a.ID, From: string(from), To: string(StateAborted),
			})
			changes.Annotations = append(changes.Annotations, core.GraphNodeAnnotation{
				TaskID: taskID, NodeID: a.ID, Metadata: map[string]interface{}{"state_reason": reason},
			})
		}
	}
	return withdrawn, changes, nil
}
