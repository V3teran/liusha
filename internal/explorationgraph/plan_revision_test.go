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

func draft(id string, triggeredBy ...string) ObjectiveDraft {
	n := derivedObjective(id, StateOpen)
	n.TaskID = revTask
	return ObjectiveDraft{Node: n, TriggeredBy: triggeredBy}
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

// 结果分析只追加方向：新方向带着催生它的结果的滋生边落图，既有的目标原样不动；
// 不存在的结果引用只剔除并如实报告。
func TestRevisePlan_NewDirectionHangsOnItsResults(t *testing.T) {
	store := revisionFixture(t)

	report, err := store.RevisePlan(context.Background(), revTask, PlanRevision{Basis: basisOf(t, store),
		AddedObjectives: []ObjectiveDraft{draft("n1", "r1", "ghost-result")},
	})
	require.NoError(t, err)

	assert.Equal(t, []string{"n1"}, report.AddedObjectives)
	assert.Equal(t, StateOpen, nodeState(t, store, "n1"))
	assert.True(t, edgesOf(t, store, RelTriggers)[[2]string{"r1", "n1"}], "滋生边 结果→新方向")
	assert.Equal(t, StateOpen, nodeState(t, store, "d1"), "修订不动既有目标")
	assert.Equal(t, StateCompleted, nodeState(t, store, "d-done"))
	require.Len(t, report.Rejected, 1)
	assert.Equal(t, FieldTriggeredBy, report.Rejected[0].Field)
	assert.Equal(t, "ghost-result", report.Rejected[0].ID)
}

func TestRevisePlan_RejectsMalformedDrafts(t *testing.T) {
	store := revisionFixture(t)
	ctx := context.Background()

	foreign := draft("n1")
	foreign.Node.TaskID = "other-task"
	notDerived := draft("n2")
	notDerived.Node.SourceType = SourceUser
	notOpen := draft("n3")
	notOpen.Node.State = stateOf(StateCompleted)

	for name, d := range map[string]ObjectiveDraft{"跨任务": foreign, "非派生": notDerived, "非 open": notOpen} {
		_, err := store.RevisePlan(ctx, revTask, PlanRevision{Basis: basisOf(t, store), AddedObjectives: []ObjectiveDraft{d}})
		assert.Error(t, err, name)
	}
	_, err := store.RevisePlan(ctx, revTask, PlanRevision{Basis: basisOf(t, store),
		AddedObjectives: []ObjectiveDraft{draft("dup"), draft("dup")},
	})
	assert.Error(t, err, "ID 重复")
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
		AddedObjectives: []ObjectiveDraft{draft("n1")},
		AddedActions:    []ActionDraft{actionDraft("a1", []string{"r1", "ghost-result"})},
	})
	require.NoError(t, err)

	assert.Equal(t, []string{"a1"}, report.AddedActions)
	assert.True(t, edgesOf(t, store, RelGenerates)[[2]string{"root", "a1"}], "动作归属根目标")
	assert.True(t, edgesOf(t, store, RelTriggers)[[2]string{"r1", "a1"}])
	require.Len(t, report.Rejected, 1, "幻觉结果引用只剔除，不拖垮修订")
	assert.Equal(t, FieldTriggeredBy, report.Rejected[0].Field)
}

// 动作挂在它所属的目标下：声明的方向是进行中的派生目标时，generates 边从它出发；没声明的
// 与声明失效的（已收敛的派生目标、根目标、不存在的目标）挂在根目标下，失效的引用剔除并
// 报告，动作照常排出。
func TestRevisePlan_ActionHangsOnItsDeclaredDirection(t *testing.T) {
	store := revisionFixture(t)
	action := func(id, direction string) ActionDraft {
		d := actionDraft(id, nil)
		d.ObjectiveID = direction
		return d
	}

	report, err := store.RevisePlan(context.Background(), revTask, PlanRevision{Basis: basisOf(t, store),
		AddedActions: []ActionDraft{
			action("a-under-d1", "d1"),
			action("a-plain", ""),
			action("a-done", "d-done"),
			action("a-root", "root"),
			action("a-ghost", "ghost-direction"),
		},
	})
	require.NoError(t, err)

	assert.Len(t, report.AddedActions, 5, "方向引用失效不拦动作")
	generates := edgesOf(t, store, RelGenerates)
	assert.True(t, generates[[2]string{"d1", "a-under-d1"}], "声明的方向：动作挂在它下")
	assert.False(t, generates[[2]string{"root", "a-under-d1"}], "一个动作只有一个所属目标")
	for _, id := range []string{"a-plain", "a-done", "a-root", "a-ghost"} {
		assert.True(t, generates[[2]string{"root", id}], "%s 挂在根目标下", id)
	}
	fields := make([]RevisionField, 0, len(report.Rejected))
	for _, r := range report.Rejected {
		fields = append(fields, r.Field)
	}
	assert.Equal(t, []RevisionField{FieldDirection, FieldDirection, FieldDirection}, fields)
}

// 修订是原子的：动作可以声明挂在同一次修订新提出的方向下，不必等它先落图。
func TestRevisePlan_ActionHangsOnADirectionAddedByTheSameRevision(t *testing.T) {
	store := revisionFixture(t)

	report, err := store.RevisePlan(context.Background(), revTask, PlanRevision{Basis: basisOf(t, store),
		AddedObjectives: []ObjectiveDraft{draft("fresh")},
		AddedActions:    []ActionDraft{{Node: actionDraft("a-fresh", nil).Node, ObjectiveID: "fresh"}},
	})
	require.NoError(t, err)

	assert.Empty(t, report.Rejected)
	assert.True(t, edgesOf(t, store, RelGenerates)[[2]string{"fresh", "a-fresh"}], "动作挂在同一次修订新提出的方向下")
}

func TestRevisePlan_FailureLeavesNothingBehind(t *testing.T) {
	store := revisionFixture(t)
	ctx := context.Background()
	clash := actionDraft("n1", nil) // 与新目标同 ID：编程错误，整体拒绝

	_, err := store.RevisePlan(ctx, revTask, PlanRevision{Basis: basisOf(t, store),
		AddedObjectives: []ObjectiveDraft{draft("n1")},
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
		Analyzed: []string{"r1"}, AddedObjectives: []ObjectiveDraft{draft("first")},
	})
	require.NoError(t, err)
	_, err = store.RevisePlan(ctx, revTask, PlanRevision{Basis: basisOf(t, store),
		Analyzed: []string{"r1"}, AddedObjectives: []ObjectiveDraft{draft("second")},
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
