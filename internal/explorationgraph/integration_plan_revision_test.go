//go:build integration

package explorationgraph

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/framework/core"
)

// TestRevisePlanPostgres 验证目标修订在 PG 后端的真实落库：superseded 状态
// 与 supersedes 边要过 DB CHECK 约束（ck_exploration_node_state /
// ck_exploration_edge_rel），并发流转引发的冲突要整体回滚——内存后端证明不了的
// 约束与事务语义在这里兜底。
func TestRevisePlanPostgres(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过集成测试")
	}

	ctx := context.Background()
	pool := setupTestDB(t)
	defer pool.Close()

	store := NewStore(pool)
	const (
		taskID   = "bbbbbbb1-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
		rootID   = "1b2f6a9e-1111-4111-8111-000000000001"
		oldID    = "1b2f6a9e-1111-4111-8111-000000000002"
		doneID   = "1b2f6a9e-1111-4111-8111-000000000003"
		resultID = "1b2f6a9e-1111-4111-8111-000000000004"
		newA     = "1b2f6a9e-1111-4111-8111-00000000000a"
		newB     = "1b2f6a9e-1111-4111-8111-00000000000b"
		newC     = "1b2f6a9e-1111-4111-8111-00000000000c"
		actionID = "1b2f6a9e-1111-4111-8111-0000000000a1"
	)
	open := StateOpen
	mk := func(id string, kind core.NodeKind, source SourceType, state *State) Node {
		return Node{ID: id, TaskID: taskID, Kind: kind, State: state, SourceType: source,
			Content: []byte(`{"description":"d","summary":"s"}`), CreatedAt: time.Now()}
	}
	for _, n := range []Node{
		mk(rootID, core.KindObjective, SourceUser, &open),
		mk(oldID, core.KindObjective, SourcePlanner, &open),
		mk(doneID, core.KindObjective, SourcePlanner, &open),
		mk(resultID, core.KindResult, SourceEvaluator, nil),
	} {
		_, err := store.CreateNode(ctx, n)
		require.NoError(t, err)
	}
	draftOf := func(id string, supersedes ...string) ObjectiveDraft {
		return ObjectiveDraft{Node: mk(id, core.KindObjective, SourcePlanner, &open),
			TriggeredBy: []string{resultID}, Supersedes: supersedes}
	}

	// 1. 拆分吸收 + 宣告达成 + 延续动作，一次修订原子落库
	report, err := store.RevisePlan(ctx, taskID, PlanRevision{
		AddedActions:        []ActionDraft{{Node: mk(actionID, core.KindAction, SourcePlanner, &open), TriggeredBy: []string{resultID}}},
		AddedObjectives:     []ObjectiveDraft{draftOf(newA, oldID, rootID), draftOf(newB, oldID)},
		CompletedObjectives: []ObjectiveCompletion{{ObjectiveID: doneID, Evidence: []string{resultID}}},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{oldID}, report.SupersededObjectives)
	assert.Equal(t, []string{doneID}, report.CompletedObjectives)
	require.Len(t, report.Rejected, 1, "根目标引用被剔除")
	assert.Equal(t, rootID, report.Rejected[0].ID)

	state := func(id string) State {
		n, gErr := store.GetNode(ctx, id)
		require.NoError(t, gErr)
		return *n.State
	}
	assert.Equal(t, StateSuperseded, state(oldID))
	assert.Equal(t, StateCompleted, state(doneID))
	assert.Equal(t, StateOpen, state(rootID))

	edges, err := store.ListEdgesByTask(ctx, taskID)
	require.NoError(t, err)
	absorbers := map[string]bool{}
	for _, e := range edges {
		if e.Rel == RelSupersedes && e.DstID == oldID {
			absorbers[e.SrcID] = true
		}
	}
	assert.Equal(t, map[string]bool{newA: true, newB: true}, absorbers)
	assert.Equal(t, []string{actionID}, report.AddedActions)
	var rootGeneratesAction bool
	for _, e := range edges {
		if e.Rel == RelGenerates && e.SrcID == rootID && e.DstID == actionID {
			rootGeneratesAction = true
		}
	}
	assert.True(t, rootGeneratesAction, "延续动作归属根目标")

	// 2. 冲突回滚：校验通过后被引用目标已流转，新节点与边一个都不落
	err = store.graphStore.ApplyChangeSet(ctx, core.GraphChangeSet{
		Nodes:       []*core.GraphNode{store.nodeToGraphNode(mk(newC, core.KindObjective, SourcePlanner, &open))},
		Transitions: []core.GraphStateTransition{{TaskID: taskID, NodeID: oldID, From: "open", To: "superseded"}},
		Edges:       []*core.GraphEdge{{TaskID: taskID, From: newC, To: oldID, Relation: string(RelSupersedes)}},
	})
	require.True(t, errors.Is(err, core.ErrGraphStateConflict), "got %v", err)
	_, err = store.GetNode(ctx, newC)
	assert.Error(t, err, "冲突时新节点随事务回滚")
}

// TestTransitionStateKeepsIdentityPostgres 锁定 PG 端的元数据合并语义：带原因的
// 状态迁移（monitor kill）只合并 blocked_reason——此前整体替换 metadata，被杀动作
// 的来源、优先级、依赖全部丢失，而内存后端是合并语义，单测看不出来。
func TestTransitionStateKeepsIdentityPostgres(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过集成测试")
	}
	ctx := context.Background()
	pool := setupTestDB(t)
	defer pool.Close()
	store := NewStore(pool)

	const (
		taskID   = "bbbbbbb2-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
		actionID = "2b2f6a9e-1111-4111-8111-000000000001"
		resultID = "2b2f6a9e-1111-4111-8111-000000000002"
	)
	open := StateOpen
	_, err := store.CreateNode(ctx, Node{ID: actionID, TaskID: taskID, Kind: core.KindAction, State: &open,
		SourceType: SourcePlanner, SourceID: "result-analyzer", Priority: PriorityHigh,
		DependsOn: []string{"2b2f6a9e-1111-4111-8111-0000000000ff"}, Content: []byte(`{"instruction":"x"}`)})
	require.NoError(t, err)
	_, err = store.CreateNode(ctx, Node{ID: resultID, TaskID: taskID, Kind: core.KindResult,
		SourceType: SourceEvaluator, Content: []byte(`{"summary":"s"}`)})
	require.NoError(t, err)

	reason := "monitor killed"
	require.NoError(t, store.TransitionState(ctx, actionID, StateAborted, &reason))
	action, err := store.GetNode(ctx, actionID)
	require.NoError(t, err)
	assert.Equal(t, StateAborted, *action.State)
	assert.Equal(t, reason, *action.BlockedReason)
	assert.Equal(t, SourcePlanner, action.SourceType)
	assert.Equal(t, PriorityHigh, action.Priority)
	assert.Equal(t, []string{"2b2f6a9e-1111-4111-8111-0000000000ff"}, action.DependsOn)
	assert.Equal(t, taskID, action.TaskID)

	_, err = store.RevisePlan(ctx, taskID, PlanRevision{Analyzed: []string{resultID}})
	require.NoError(t, err)
	result, err := store.GetNode(ctx, resultID)
	require.NoError(t, err)
	assert.True(t, result.IsAnalyzed())
	assert.Equal(t, SourceEvaluator, result.SourceType, "水位批注不触碰其余元数据")
}

// TestRevisePlanConsumesOncePostgres 锁定 PG 端分析水位的一次性语义：同一结果
// 被第二次修订消费时整体回滚（键存在 → 冲突），不存在的结果仍报 NotFound 语义。
func TestRevisePlanConsumesOncePostgres(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过集成测试")
	}
	ctx := context.Background()
	pool := setupTestDB(t)
	defer pool.Close()
	store := NewStore(pool)

	const (
		taskID   = "bbbbbbb3-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
		rootID   = "3b2f6a9e-1111-4111-8111-000000000001"
		resultID = "3b2f6a9e-1111-4111-8111-000000000002"
		firstID  = "3b2f6a9e-1111-4111-8111-00000000000a"
		secondID = "3b2f6a9e-1111-4111-8111-00000000000b"
	)
	open := StateOpen
	_, err := store.CreateNode(ctx, Node{ID: rootID, TaskID: taskID, Kind: core.KindObjective, State: &open,
		SourceType: SourceUser, Content: []byte(`{"description":"root"}`)})
	require.NoError(t, err)
	_, err = store.CreateNode(ctx, Node{ID: resultID, TaskID: taskID, Kind: core.KindResult,
		SourceType: SourceEvaluator, Content: []byte(`{"summary":"s"}`)})
	require.NoError(t, err)
	objective := func(id string) ObjectiveDraft {
		return ObjectiveDraft{Node: Node{ID: id, TaskID: taskID, Kind: core.KindObjective, State: &open,
			SourceType: SourcePlanner, Content: []byte(`{"description":"d"}`)}}
	}

	_, err = store.RevisePlan(ctx, taskID, PlanRevision{Analyzed: []string{resultID}, AddedObjectives: []ObjectiveDraft{objective(firstID)}})
	require.NoError(t, err)
	_, err = store.RevisePlan(ctx, taskID, PlanRevision{Analyzed: []string{resultID}, AddedObjectives: []ObjectiveDraft{objective(secondID)}})
	require.True(t, errors.Is(err, core.ErrGraphStateConflict), "got %v", err)
	_, err = store.GetNode(ctx, secondID)
	assert.Error(t, err, "后到的修订整体回滚")
}
