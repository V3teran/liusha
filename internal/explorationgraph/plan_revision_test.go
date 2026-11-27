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

const revTask = "task-rev"

func stateOf(s State) *State { return &s }

// seedNode 建节点，失败即刻终止测试。
func seedNode(t *testing.T, store *Store, n Node) {
	t.Helper()
	if n.TaskID == "" {
		n.TaskID = revTask
	}
	if n.CreatedAt.IsZero() {
		n.CreatedAt = time.Now()
	}
	_, err := store.CreateNode(context.Background(), n)
	require.NoError(t, err, "建节点 %s", n.ID)
}

func rootObjective(id string) Node {
	return Node{ID: id, Kind: core.KindObjective, State: stateOf(StateOpen),
		SourceType: SourceUser, Content: []byte(`{"description":"根"}`)}
}

func derivedObjective(id string, state State) Node {
	return Node{ID: id, Kind: core.KindObjective, State: stateOf(state),
		SourceType: SourcePlanner, Content: []byte(`{"description":"派生"}`)}
}

func resultNode(id string) Node {
	return Node{ID: id, Kind: core.KindResult, SourceType: SourceEvaluator, Content: []byte(`{"statement":"r"}`)}
}

func draft(id string, triggeredBy, supersedes []string) ObjectiveDraft {
	n := derivedObjective(id, StateOpen)
	n.TaskID = revTask
	return ObjectiveDraft{Node: n, TriggeredBy: triggeredBy, Supersedes: supersedes}
}

// revisionFixture: 根目标 root、派生目标 d1/d2（open）、d-done（completed）、结果 r1。
func revisionFixture(t *testing.T) *Store {
	t.Helper()
	store := NewMemoryStore()
	seedNode(t, store, rootObjective("root"))
	seedNode(t, store, derivedObjective("d1", StateOpen))
	seedNode(t, store, derivedObjective("d2", StateOpen))
	seedNode(t, store, derivedObjective("d-done", StateCompleted))
	seedNode(t, store, resultNode("r1"))
	return store
}

func nodeState(t *testing.T, store *Store, id string) State {
	t.Helper()
	n, err := store.GetNode(context.Background(), id)
	require.NoError(t, err)
	require.NotNil(t, n.State)
	return *n.State
}

func edgesOf(t *testing.T, store *Store, rel Relation) map[[2]string]bool {
	t.Helper()
	edges, err := store.ListEdgesByTask(context.Background(), revTask)
	require.NoError(t, err)
	set := map[[2]string]bool{}
	for _, e := range edges {
		if e.Rel == rel {
			set[[2]string{e.SrcID, e.DstID}] = true
		}
	}
	return set
}

func TestRevisePlan_SupersedeLandsStateAndEdgeTogether(t *testing.T) {
	store := revisionFixture(t)

	report, err := store.RevisePlan(context.Background(), revTask, PlanRevision{Basis: basisOf(t, store),
		AddedObjectives: []ObjectiveDraft{draft("n1", []string{"r1"}, []string{"d1"})},
	})
	require.NoError(t, err)

	assert.Equal(t, []string{"n1"}, report.AddedObjectives)
	assert.Equal(t, []string{"d1"}, report.SupersededObjectives)
	assert.Empty(t, report.Rejected)
	assert.Equal(t, StateSuperseded, nodeState(t, store, "d1"))
	assert.Equal(t, StateOpen, nodeState(t, store, "n1"))
	assert.True(t, edgesOf(t, store, RelSupersedes)[[2]string{"n1", "d1"}], "吸收边 新→旧")
	assert.True(t, edgesOf(t, store, RelTriggers)[[2]string{"r1", "n1"}], "滋生边 结果→新目标")
}

func TestRevisePlan_SplitRecordsEveryAbsorber(t *testing.T) {
	store := revisionFixture(t)

	report, err := store.RevisePlan(context.Background(), revTask, PlanRevision{Basis: basisOf(t, store),
		AddedObjectives: []ObjectiveDraft{
			draft("n1", nil, []string{"d1"}),
			draft("n2", nil, []string{"d1", "d1"}),
		},
	})
	require.NoError(t, err)

	assert.Equal(t, []string{"d1"}, report.SupersededObjectives, "旧目标只迁移一次")
	assert.Empty(t, report.Rejected, "拆分不是无效引用")
	supersedes := edgesOf(t, store, RelSupersedes)
	assert.True(t, supersedes[[2]string{"n1", "d1"}])
	assert.True(t, supersedes[[2]string{"n2", "d1"}])
	assert.Len(t, supersedes, 2)
}

func TestRevisePlan_RejectsInvalidReferencesWithoutBlockingTheRest(t *testing.T) {
	store := revisionFixture(t)
	seedNode(t, store, Node{ID: "foreign", TaskID: "other-task", Kind: core.KindObjective,
		State: stateOf(StateOpen), SourceType: SourcePlanner, CreatedAt: time.Now()})
	seedNode(t, store, Node{ID: "a1", Kind: core.KindAction, State: stateOf(StateOpen), SourceType: SourcePlanner})

	report, err := store.RevisePlan(context.Background(), revTask, PlanRevision{Basis: basisOf(t, store),
		AddedObjectives: []ObjectiveDraft{
			draft("n1", []string{"r1", "ghost-result"}, []string{"root", "d-done", "foreign", "a1", "ghost", "d2"}),
		},
	})
	require.NoError(t, err)

	assert.Equal(t, []string{"n1"}, report.AddedObjectives, "无效引用不拖垮新目标")
	assert.Equal(t, []string{"d2"}, report.SupersededObjectives, "有效引用照常吸收")
	rejected := map[string]RevisionField{}
	for _, r := range report.Rejected {
		rejected[r.ID] = r.Field
	}
	assert.Equal(t, map[string]RevisionField{
		"ghost-result": FieldTriggeredBy,
		"root":         FieldSupersedes,
		"d-done":       FieldSupersedes,
		"foreign":      FieldSupersedes,
		"a1":           FieldSupersedes,
		"ghost":        FieldSupersedes,
	}, rejected)
	assert.Equal(t, StateOpen, nodeState(t, store, "root"), "根目标不受修订影响")
	assert.Equal(t, StateCompleted, nodeState(t, store, "d-done"))
}

func TestRevisePlan_CompletionNeedsDerivedOpenTargetAndEvidence(t *testing.T) {
	store := revisionFixture(t)

	report, err := store.RevisePlan(context.Background(), revTask, PlanRevision{Basis: basisOf(t, store),
		CompletedObjectives: []ObjectiveCompletion{
			{ObjectiveID: "d1", Evidence: []string{"r1"}},
			{ObjectiveID: "d2", Evidence: []string{"ghost-result"}},
			{ObjectiveID: "root", Evidence: []string{"r1"}},
		},
	})
	require.NoError(t, err)

	assert.Equal(t, []string{"d1"}, report.CompletedObjectives)
	assert.Equal(t, StateCompleted, nodeState(t, store, "d1"))
	assert.Equal(t, StateOpen, nodeState(t, store, "d2"), "无有效证据不成立")
	assert.Equal(t, StateOpen, nodeState(t, store, "root"), "根目标不能被宣告达成")
	assert.Len(t, report.Rejected, 3) // ghost-result 证据 + d2 + root
}

func TestRevisePlan_CompletionWinsOverSupersedeOfSameObjective(t *testing.T) {
	store := revisionFixture(t)

	report, err := store.RevisePlan(context.Background(), revTask, PlanRevision{Basis: basisOf(t, store),
		CompletedObjectives: []ObjectiveCompletion{{ObjectiveID: "d1", Evidence: []string{"r1"}}},
		AddedObjectives:     []ObjectiveDraft{draft("n1", nil, []string{"d1"})},
	})
	require.NoError(t, err)

	assert.Equal(t, StateCompleted, nodeState(t, store, "d1"))
	assert.Empty(t, report.SupersededObjectives)
	assert.Empty(t, edgesOf(t, store, RelSupersedes), "未迁移就不落吸收边")
}

func TestRevisePlan_RejectsMalformedDrafts(t *testing.T) {
	store := revisionFixture(t)
	ctx := context.Background()

	foreign := draft("n1", nil, nil)
	foreign.Node.TaskID = "other-task"
	notDerived := draft("n2", nil, nil)
	notDerived.Node.SourceType = SourceUser
	notOpen := draft("n3", nil, nil)
	notOpen.Node.State = stateOf(StateCompleted)

	for name, d := range map[string]ObjectiveDraft{"跨任务": foreign, "非派生": notDerived, "非 open": notOpen} {
		_, err := store.RevisePlan(ctx, revTask, PlanRevision{Basis: basisOf(t, store), AddedObjectives: []ObjectiveDraft{d}})
		assert.Error(t, err, name)
	}
	_, err := store.RevisePlan(ctx, revTask, PlanRevision{Basis: basisOf(t, store),
		AddedObjectives: []ObjectiveDraft{draft("dup", nil, nil), draft("dup", nil, nil)},
	})
	assert.Error(t, err, "ID 重复")
}

func TestApplyChangeSet_ConflictRollsBackEverything(t *testing.T) {
	store := revisionFixture(t)
	ctx := context.Background()

	n := derivedObjective("n1", StateOpen)
	n.TaskID = revTask
	err := store.graphStore.ApplyChangeSet(ctx, core.GraphChangeSet{
		Nodes: []*core.GraphNode{store.nodeToGraphNode(n)},
		Transitions: []core.GraphStateTransition{
			{TaskID: revTask, NodeID: "d1", From: "open", To: "superseded"},
			{TaskID: revTask, NodeID: "d-done", From: "open", To: "superseded"}, // 前置不成立
		},
		Edges: []*core.GraphEdge{{TaskID: revTask, From: "n1", To: "d1", Relation: string(RelSupersedes)}},
	})
	require.True(t, errors.Is(err, core.ErrGraphStateConflict), "got %v", err)

	_, getErr := store.GetNode(ctx, "n1")
	assert.Error(t, getErr, "新节点随冲突回滚")
	assert.Equal(t, StateOpen, nodeState(t, store, "d1"), "已校验通过的迁移也回滚")
	assert.Empty(t, edgesOf(t, store, RelSupersedes))
}

func TestIsDerivedObjective(t *testing.T) {
	root, derived, action := rootObjective("a"), derivedObjective("b", StateOpen), Node{Kind: core.KindAction, SourceType: SourcePlanner}
	assert.False(t, root.IsDerivedObjective())
	assert.True(t, derived.IsDerivedObjective())
	assert.False(t, action.IsDerivedObjective())
}

func actionDraft(id string, triggeredBy []string) ActionDraft {
	return ActionDraft{
		Node: Node{ID: id, TaskID: revTask, Kind: core.KindAction, State: stateOf(StateOpen),
			SourceType: SourcePlanner, Content: []byte(`{"instruction":"深挖"}`)},
		TriggeredBy: triggeredBy,
	}
}

func TestRevisePlan_ActionsHangOnRootAndShareTheTransaction(t *testing.T) {
	store := revisionFixture(t)

	report, err := store.RevisePlan(context.Background(), revTask, PlanRevision{Basis: basisOf(t, store),
		AddedObjectives: []ObjectiveDraft{draft("n1", nil, []string{"d1"})},
		AddedActions:    []ActionDraft{actionDraft("a1", []string{"r1", "ghost-result"})},
	})
	require.NoError(t, err)

	assert.Equal(t, []string{"a1"}, report.AddedActions)
	assert.True(t, edgesOf(t, store, RelGenerates)[[2]string{"root", "a1"}], "动作归属根目标")
	assert.True(t, edgesOf(t, store, RelTriggers)[[2]string{"r1", "a1"}])
	require.Len(t, report.Rejected, 1, "幻觉结果引用只剔除，不拖垮修订")
	assert.Equal(t, FieldTriggeredBy, report.Rejected[0].Field)
}

// 规划修订整体失败时什么都不落：重试不会重复写入已生效的一半。
func TestRevisePlan_FailureLeavesNothingBehind(t *testing.T) {
	store := revisionFixture(t)
	ctx := context.Background()
	clash := actionDraft("n1", nil) // 与新目标同 ID：编程错误，整体拒绝

	_, err := store.RevisePlan(ctx, revTask, PlanRevision{Basis: basisOf(t, store),
		AddedObjectives: []ObjectiveDraft{draft("n1", nil, []string{"d1"})},
		AddedActions:    []ActionDraft{clash},
	})
	require.Error(t, err)
	_, getErr := store.GetNode(ctx, "n1")
	assert.Error(t, getErr)
	assert.Equal(t, StateOpen, nodeState(t, store, "d1"))
}

func TestRevisePlan_ActionsNeedARootObjective(t *testing.T) {
	store := NewMemoryStore()
	seedNode(t, store, derivedObjective("d1", StateOpen))

	_, err := store.RevisePlan(context.Background(), revTask, PlanRevision{Basis: GoalBasis{ObjectiveID: "d1", Version: 1},
		AddedActions: []ActionDraft{actionDraft("a1", nil)},
	})
	assert.Error(t, err)
	_, getErr := store.GetNode(context.Background(), "a1")
	assert.Error(t, getErr)
}

func TestRevisePlan_AnalyzedWatermarkCommitsWithTheRevision(t *testing.T) {
	store := revisionFixture(t)
	ctx := context.Background()

	_, err := store.RevisePlan(ctx, revTask, PlanRevision{Basis: basisOf(t, store), Analyzed: []string{"r1"}})
	require.NoError(t, err, "空结论的分析也要落水位")
	r1, err := store.GetNode(ctx, "r1")
	require.NoError(t, err)
	assert.True(t, r1.IsAnalyzed())

	_, err = store.RevisePlan(ctx, revTask, PlanRevision{Basis: basisOf(t, store), Analyzed: []string{"ghost-result"}})
	assert.Error(t, err, "水位只能落在本任务的结果上")
}

// 否决只合并 state_reason / rejected_by，节点身份字段（来源/优先级）不丢。
func TestRejectAction_KeepsNodeIdentity(t *testing.T) {
	store := revisionFixture(t)
	ctx := context.Background()
	seedNode(t, store, Node{ID: "a1", Kind: core.KindAction, State: stateOf(StateOpen),
		SourceType: SourcePlanner, Priority: PriorityHigh})

	reason := "偏离目标"
	ok, err := store.RejectAction(ctx, revTask, "a1", SourceMonitor, RejectStep, reason)
	require.NoError(t, err)
	require.True(t, ok)

	a1, err := store.GetNode(ctx, "a1")
	require.NoError(t, err)
	assert.Equal(t, StateRejected, *a1.State)
	assert.Equal(t, reason, *a1.StateReason)
	assert.Equal(t, SourcePlanner, a1.SourceType)
	assert.Equal(t, PriorityHigh, a1.Priority)
	assert.Equal(t, revTask, a1.TaskID)
}

func TestRootObjective_IsEarliestNonDerived(t *testing.T) {
	root, ok := RootObjective([]Node{derivedObjective("d", StateOpen), rootObjective("r1"), rootObjective("r2")})
	require.True(t, ok)
	assert.Equal(t, "r1", root.ID)
	_, ok = RootObjective([]Node{derivedObjective("d", StateOpen)})
	assert.False(t, ok)
}

// 两次分析并发消费同一结果（新旧 run 短暂重叠）：水位是一次性批注，后提交的
// 修订整体回滚，它提出的新目标不会重复落图。
func TestRevisePlan_ConcurrentConsumersCommitOnce(t *testing.T) {
	store := revisionFixture(t)
	ctx := context.Background()

	_, err := store.RevisePlan(ctx, revTask, PlanRevision{Basis: basisOf(t, store),
		Analyzed: []string{"r1"}, AddedObjectives: []ObjectiveDraft{draft("first", nil, nil)},
	})
	require.NoError(t, err)
	_, err = store.RevisePlan(ctx, revTask, PlanRevision{Basis: basisOf(t, store),
		Analyzed: []string{"r1"}, AddedObjectives: []ObjectiveDraft{draft("second", nil, nil)},
	})
	require.True(t, errors.Is(err, core.ErrGraphStateConflict), "got %v", err)
	_, getErr := store.GetNode(ctx, "second")
	assert.Error(t, getErr, "后到的修订整体回滚")
}

// rewriteGoal 改写 fixture 根目标的描述（生产路径经 task.Store.SetGoal 与任务行同事务写入）。
func rewriteGoal(t *testing.T, store *Store, goal string) {
	t.Helper()
	require.NoError(t, store.UpdateNodeContent(context.Background(), "root", MustJSON(map[string]string{"description": goal})))
}

// basisOf 读取任务当前的目标依据。
func basisOf(t *testing.T, store *Store) GoalBasis {
	t.Helper()
	basis, err := store.GoalBasis(context.Background(), revTask)
	require.NoError(t, err)
	return basis
}

// 规划修订以「规划所依据的目标」为前置：规划期间目标被改写，整批作废、什么都不落。
func TestRevisePlan_DiscardedWhenGoalChangedSinceBasis(t *testing.T) {
	store := revisionFixture(t)
	ctx := context.Background()
	basis := basisOf(t, store)

	rewriteGoal(t, store, "新目标")
	_, err := store.RevisePlan(ctx, revTask, PlanRevision{
		Basis:        basis,
		Analyzed:     []string{"r1"},
		AddedActions: []ActionDraft{actionDraft("a1", nil)},
	})
	require.ErrorIs(t, err, ErrGoalChanged)
	_, getErr := store.GetNode(ctx, "a1")
	assert.Error(t, getErr, "按旧目标做出的动作不落图")
	r1, err := store.GetNode(ctx, "r1")
	require.NoError(t, err)
	assert.False(t, r1.IsAnalyzed(), "分析水位随修订一同作废，按新目标重新分析")

	_, err = store.RevisePlan(ctx, revTask, PlanRevision{
		Basis: basisOf(t, store), AddedActions: []ActionDraft{actionDraft("a1", nil)},
	})
	require.NoError(t, err, "按新依据重做即可落图")
}

// 修订必须声明依据：没有依据的规划结论无从判断是否仍然成立。
func TestRevisePlan_RequiresBasis(t *testing.T) {
	store := revisionFixture(t)
	_, err := store.RevisePlan(context.Background(), revTask, PlanRevision{
		AddedActions: []ActionDraft{actionDraft("a1", nil)},
	})
	require.Error(t, err)
	_, getErr := store.GetNode(context.Background(), "a1")
	assert.Error(t, getErr)
}

// 目标依据随目标改写与任务状态迁移变化，其余图写入（动作、批注）不影响它。
func TestGoalBasis_TracksGoalOnly(t *testing.T) {
	store := revisionFixture(t)
	ctx := context.Background()
	before := basisOf(t, store)
	assert.Equal(t, "root", before.ObjectiveID)

	_, err := store.RevisePlan(ctx, revTask, PlanRevision{Basis: before, AddedActions: []ActionDraft{actionDraft("a1", nil)}})
	require.NoError(t, err)
	assert.Equal(t, before, basisOf(t, store), "动作挂到根目标下不改变依据")

	rewriteGoal(t, store, "新目标")
	assert.NotEqual(t, before, basisOf(t, store), "目标改写后依据随之变化")

	_, err = NewMemoryStore().GoalBasis(ctx, revTask)
	assert.Error(t, err, "没有根目标就没有依据")
}
