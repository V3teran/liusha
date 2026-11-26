//go:build integration

package explorationgraph

import (
	"context"
	"errors"
	"github.com/V3teran/liusha/internal/dbtest"
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
	dbtest.SeedTask(t, pool, taskID)
	open := StateOpen
	mk := func(id string, kind core.NodeKind, source SourceType, state *State) Node {
		return Node{ID: id, TaskID: taskID, Kind: kind, State: state, SourceType: source,
			Content: []byte(`{"description":"d","statement":"s"}`), CreatedAt: time.Now()}
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

	// 1. 拆分吸收 + 宣告达成 + 新动作，一次修订原子落库
	report, err := store.RevisePlan(ctx, taskID, PlanRevision{Basis: pgBasis(t, store, taskID),
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
	assert.True(t, rootGeneratesAction, "动作归属根目标")

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
// 否决与分析水位都只合并各自的键（state_reason / rejected_by、analyzed）：整体替换
// metadata 会抹掉节点的来源、优先级等身份字段，而内存后端是合并语义，单测看不出来。
func TestRejectAndWatermarkKeepIdentityPostgres(t *testing.T) {
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
	dbtest.SeedTask(t, pool, taskID)
	open := StateOpen
	_, err := store.CreateNode(ctx, Node{ID: "2b2f6a9e-1111-4111-8111-0000000000ff", TaskID: taskID, Kind: core.KindObjective,
		State: &open, SourceType: SourceUser, Content: []byte(`{"description":"root"}`)})
	require.NoError(t, err)
	_, err = store.CreateNode(ctx, Node{ID: actionID, TaskID: taskID, Kind: core.KindAction, State: &open,
		SourceType: SourcePlanner, Priority: PriorityHigh,
		Content: []byte(`{"instruction":"x"}`)})
	require.NoError(t, err)
	_, err = store.CreateNode(ctx, Node{ID: resultID, TaskID: taskID, Kind: core.KindResult,
		SourceType: SourceEvaluator, Content: []byte(`{"statement":"s"}`)})
	require.NoError(t, err)

	reason := "偏离目标"
	ok, err := store.RejectAction(ctx, taskID, actionID, SourceMonitor, reason)
	require.NoError(t, err)
	require.True(t, ok)
	action, err := store.GetNode(ctx, actionID)
	require.NoError(t, err)
	assert.Equal(t, StateRejected, *action.State)
	assert.Equal(t, SourceMonitor, action.RejectedBy())
	assert.Equal(t, reason, *action.StateReason)
	assert.Equal(t, SourcePlanner, action.SourceType)
	assert.Equal(t, PriorityHigh, action.Priority)
	assert.Equal(t, taskID, action.TaskID)

	_, err = store.RevisePlan(ctx, taskID, PlanRevision{Basis: pgBasis(t, store, taskID), Analyzed: []string{resultID}})
	require.NoError(t, err)
	result, err := store.GetNode(ctx, resultID)
	require.NoError(t, err)
	assert.True(t, result.IsAnalyzed())
	assert.Equal(t, SourceEvaluator, result.SourceType, "水位批注不触碰其余元数据")
}

// 线索的两种处置在 PG 下真实落图：动作认领一条线索，RevisePlan 落一条 pursues 边
// （action → 线索），线索因被在办动作跟进而退出待深挖清单；另一条线索被判定不追，理由
// 批注在它身上（metadata 浅合并），进入已不追。opens_deeper 标志与节点来源都不受影响。
func TestLeadPursuitAndDeclinePostgres(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过集成测试")
	}
	ctx := context.Background()
	pool := setupTestDB(t)
	defer pool.Close()
	store := NewStore(pool)

	const (
		taskID    = "bbbbbbb3-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
		rootID    = "3b2f6a9e-1111-4111-8111-0000000000ff"
		pursuedID = "3b2f6a9e-1111-4111-8111-000000000001"
		actionID  = "3b2f6a9e-1111-4111-8111-000000000002"
		declined  = "3b2f6a9e-1111-4111-8111-000000000003"
	)
	dbtest.SeedTask(t, pool, taskID)
	open := StateOpen
	_, err := store.CreateNode(ctx, Node{ID: rootID, TaskID: taskID, Kind: core.KindObjective,
		State: &open, SourceType: SourceUser, Content: []byte(`{"description":"root"}`)})
	require.NoError(t, err)
	for _, id := range []string{pursuedID, declined} {
		_, err = store.CreateNode(ctx, Node{ID: id, TaskID: taskID, Kind: core.KindObservation,
			SourceType: SourceExecutor, Content: []byte(`{"category":"access","statement":"探通一条路"}`),
			Metadata: OpensDeeperMetadata()})
		require.NoError(t, err)
	}

	openLeads, err := store.OpenLeads(ctx, taskID)
	require.NoError(t, err)
	require.Len(t, openLeads, 2, "落库后两条都是待深挖线索")

	action := Node{ID: actionID, TaskID: taskID, Kind: core.KindAction, State: &open,
		SourceType: SourcePlanner, Content: []byte(`{"instruction":"深挖"}`)}
	report, err := store.RevisePlan(ctx, taskID, PlanRevision{Basis: pgBasis(t, store, taskID),
		AddedActions:  []ActionDraft{{Node: action, PursuesLead: pursuedID}},
		DeclinedLeads: []LeadDecline{{LeadID: declined, Reason: "超出任务目标"}},
	})
	require.NoError(t, err)
	assert.Empty(t, report.Rejected)
	assert.Equal(t, []string{declined}, report.DeclinedLeads)

	edges, err := store.ListEdgesByTask(ctx, taskID)
	require.NoError(t, err)
	var pursues *Edge
	for i := range edges {
		if edges[i].Rel == RelPursues {
			pursues = &edges[i]
		}
	}
	require.NotNil(t, pursues, "落一条 pursues 边")
	assert.Equal(t, actionID, pursues.SrcID, "边起点是动作")
	assert.Equal(t, pursuedID, pursues.DstID, "边终点是线索")

	pursued, err := store.PursuedLeads(ctx, taskID, actionID)
	require.NoError(t, err)
	require.Len(t, pursued, 1, "按动作查得到它认领的线索")
	assert.Equal(t, pursuedID, pursued[0].ID)

	openLeads, err = store.OpenLeads(ctx, taskID)
	require.NoError(t, err)
	assert.Empty(t, openLeads, "一条被在办动作跟进、一条已不追，都不再待深挖")

	declinedLeads, err := store.DeclinedLeads(ctx, taskID)
	require.NoError(t, err)
	require.Len(t, declinedLeads, 1)
	assert.Equal(t, declined, declinedLeads[0].ID)
	assert.Equal(t, "超出任务目标", declinedLeads[0].LeadDeclineReason(), "理由经批注落库并可读回")

	for id, wantReason := range map[string]string{pursuedID: "", declined: "超出任务目标"} {
		lead, err := store.GetNode(ctx, id)
		require.NoError(t, err)
		assert.True(t, lead.OpensDeeper(), "处置不抹掉 opens_deeper 标志")
		assert.Equal(t, SourceExecutor, lead.SourceType, "处置不触碰节点来源")
		assert.Equal(t, wantReason, lead.LeadDeclineReason())
	}
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
	dbtest.SeedTask(t, pool, taskID)
	open := StateOpen
	_, err := store.CreateNode(ctx, Node{ID: rootID, TaskID: taskID, Kind: core.KindObjective, State: &open,
		SourceType: SourceUser, Content: []byte(`{"description":"root"}`)})
	require.NoError(t, err)
	_, err = store.CreateNode(ctx, Node{ID: resultID, TaskID: taskID, Kind: core.KindResult,
		SourceType: SourceEvaluator, Content: []byte(`{"statement":"s"}`)})
	require.NoError(t, err)
	objective := func(id string) ObjectiveDraft {
		return ObjectiveDraft{Node: Node{ID: id, TaskID: taskID, Kind: core.KindObjective, State: &open,
			SourceType: SourcePlanner, Content: []byte(`{"description":"d"}`)}}
	}

	_, err = store.RevisePlan(ctx, taskID, PlanRevision{Basis: pgBasis(t, store, taskID), Analyzed: []string{resultID}, AddedObjectives: []ObjectiveDraft{objective(firstID)}})
	require.NoError(t, err)
	_, err = store.RevisePlan(ctx, taskID, PlanRevision{Basis: pgBasis(t, store, taskID), Analyzed: []string{resultID}, AddedObjectives: []ObjectiveDraft{objective(secondID)}})
	require.True(t, errors.Is(err, core.ErrGraphStateConflict), "got %v", err)
	_, err = store.GetNode(ctx, secondID)
	assert.Error(t, err, "后到的修订整体回滚")
}

// pgBasis 读取任务当前的目标依据。
func pgBasis(t *testing.T, store *Store, taskID string) GoalBasis {
	t.Helper()
	basis, err := store.GoalBasis(context.Background(), taskID)
	require.NoError(t, err)
	return basis
}
