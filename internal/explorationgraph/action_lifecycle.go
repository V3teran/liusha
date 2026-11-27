package explorationgraph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/V3teran/liusha/internal/framework/core"
)

// 动作的执行生命周期：open →(认领)→ running →(结束)→ completed/exhausted/failed，
// 或 running →(交还)→ open；open / running 都可被否决（→ rejected）。
//
// 认领记下执行它的 run（metaClaimedBy），结束与交还都以「仍由我认领」为前置：同一
// 任务的新旧 run 可能短暂重叠（恢复重投、僵尸回收），新 run 回收并重新认领后，旧 run
// 迟到的结果不得改写新 run 的认领。外部决定（否决、任务收口）把动作迁出 running，
// 执行方的结束随之落空，不会覆盖外部决定。
//
// 中断不是失败：run 被暂停、停机或超时打断时，在途动作交还 open，由下一个 run 重新
// 执行；任务进入终态时由收尾统一处置仍未结束的动作。

// metaClaimedBy 是认领者（agent_run.id）在 action metadata 中的键。
const metaClaimedBy = "claimed_by"

// metaRejectedBy 是否决方（SourceMonitor / SourceUser）在 action metadata 中的键。
const metaRejectedBy = "rejected_by"

// metaRejectedScope 是否决范围（RejectScope）在 action metadata 中的键。
const metaRejectedScope = "rejected_scope"

// ErrActionNotFound 表示本任务下没有这个动作（节点不存在、属于别的任务或不是动作）。
var ErrActionNotFound = errors.New("动作不存在")

// RejectScope 是一次否决评判的范围。否决评判的是一个动作，但不一定是它在跟进的线索：
// 范围随否决落图，线索的处境据此推导（见 lead.go）。
type RejectScope string

const (
	// RejectStep：这一步不该这么做（重复、手法不当）。它跟进的线索带着否决理由重新待深挖
	// 跟进——下一次跟进不得与被否决的这一次等效。
	RejectStep RejectScope = "step"
	// RejectLead：这条线本身离题或越界。被否决的动作所跟进的线索随之关闭。
	RejectLead RejectScope = "lead"
)

// RejectedBy 返回否决该动作的一方（未被否决为空）。
func (n *Node) RejectedBy() SourceType {
	if len(n.Metadata) == 0 {
		return ""
	}
	var m struct {
		RejectedBy SourceType `json:"rejected_by"`
	}
	if json.Unmarshal(n.Metadata, &m) != nil {
		return ""
	}
	return m.RejectedBy
}

// RejectionScope 返回否决该动作的范围（没记时按只否决这一步）。
func (n *Node) RejectionScope() RejectScope {
	if len(n.Metadata) == 0 {
		return RejectStep
	}
	var m struct {
		Scope RejectScope `json:"rejected_scope"`
	}
	if json.Unmarshal(n.Metadata, &m) != nil || m.Scope != RejectLead {
		return RejectStep
	}
	return RejectLead
}

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

// RejectAction 否决一个待执行或执行中的动作（→ rejected），记下否决方、范围与原因。
// 范围为空按只否决这一步。执行中的动作由执行方的取消开关感知后中断，其迟到的结束因
// 认领前置不成立而落空。动作已结束返回 false；不是本任务的动作返回 ErrActionNotFound。
func (s *Store) RejectAction(ctx context.Context, taskID, actionID string, by SourceType, scope RejectScope, reason string) (bool, error) {
	node, err := s.GetNode(ctx, actionID)
	if err != nil {
		return false, fmt.Errorf("%w: %s（%w）", ErrActionNotFound, actionID, err)
	}
	if node.TaskID != taskID || node.Kind != core.KindAction {
		return false, fmt.Errorf("%w: %s", ErrActionNotFound, actionID)
	}
	if scope == "" {
		scope = RejectStep
	}
	annotation := core.GraphNodeAnnotation{
		TaskID: taskID, NodeID: actionID,
		Metadata: map[string]interface{}{
			"state_reason": reason, metaRejectedBy: string(by), metaRejectedScope: string(scope),
		},
	}
	// 两次 CAS 覆盖并发认领：open 在两次尝试之间被认领，第二次按 running 命中；
	// 两次都落空即动作已结束。
	for _, from := range []State{StateOpen, StateRunning} {
		ok, err := s.applyActionChange(ctx, core.GraphChangeSet{
			Transitions: []core.GraphStateTransition{{
				TaskID: taskID, NodeID: actionID, From: string(from), To: string(StateRejected),
			}},
			Annotations: []core.GraphNodeAnnotation{annotation},
		})
		if err != nil || ok {
			return ok, err
		}
	}
	return false, nil
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
