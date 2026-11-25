package monitor

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/monitor/metrics"
)

// replanRecorder 记录收到的重规划请求（规划者的替身）。
type replanRecorder struct {
	reasons []string
	seen    []*explorationgraph.Surface
}

func (r *replanRecorder) RequestReplan(reason string, seen *explorationgraph.Surface) {
	r.reasons = append(r.reasons, reason)
	r.seen = append(r.seen, seen)
}

// observedCtx 模拟一轮评估：带上本轮的所见记录，并先看一次全局态势。
func observedCtx(t *testing.T, graph *explorationgraph.Store, taskID string) context.Context {
	t.Helper()
	ctx := withSeenSurface(context.Background())
	tool := NewGetGlobalStateTool(graph, taskID, metrics.NewCollector(graph, taskID, zerolog.Nop()))
	out, err := tool.Execute(ctx, json.RawMessage(`{}`))
	require.NoError(t, err)
	require.Empty(t, out.Error)
	return ctx
}

func decisionArgs(t *testing.T, d Decision) json.RawMessage {
	t.Helper()
	args, err := json.Marshal(d)
	require.NoError(t, err)
	return args
}

// request_replan 直接交给规划者（控制信号不走可丢的事件总线，也不写进图），并带上监察者
// 作出判断时所见的图面：规划者处理时据它判断请求是否已过时。理由里写明问题类别与证据。
func TestPublishDecision_RequestReplanReachesPlannerWithSeenSurface(t *testing.T) {
	graph := newGraphWithActions(t)
	ctx := observedCtx(t, graph, "t1")
	planner := &replanRecorder{}
	tool := NewPublishDecisionTool(graph, planner, "t1")

	out, err := tool.Execute(ctx, decisionArgs(t, Decision{
		Type: DecisionRequestReplan, Problem: ProblemStalled, EvidenceIDs: []string{"a-done", "a-running"},
		Reason: "同一处反复尝试没有新结果",
	}))
	require.NoError(t, err)
	require.Empty(t, out.Error)

	require.Len(t, planner.reasons, 1)
	assert.Equal(t, "探索停滞：同一处反复尝试没有新结果（证据：a-done、a-running）", planner.reasons[0])
	require.NotNil(t, planner.seen[0], "请求带上所见图面")
	assert.Equal(t, 3, planner.seen[0].Actions)

	node, err := graph.GetNode(context.Background(), "obj-1")
	require.NoError(t, err)
	assert.JSONEq(t, `{"description":"测试目标"}`, string(node.Content), "重规划请求不写进图")
}

// 决策必须基于本轮亲见的态势：没看过全局态势就发决策，拒绝。
func TestPublishDecision_RequiresObservedState(t *testing.T) {
	graph := newGraphWithActions(t)
	planner := &replanRecorder{}
	tool := NewPublishDecisionTool(graph, planner, "t1")

	out, err := tool.Execute(withSeenSurface(context.Background()), decisionArgs(t, Decision{
		Type: DecisionRequestReplan, Problem: ProblemStalled, EvidenceIDs: []string{"a-done"}, Reason: "停滞",
	}))
	require.NoError(t, err)
	assert.Contains(t, out.Error, "task_survey")
	assert.Empty(t, planner.reasons)
}

// 决策必须指明是哪一类功能障碍、引用本任务里真实存在的证据：「无需干预」这类没有问题的
// 判断表达不成一次决策（实测：监察者写着「本轮无需干预」却请求了重规划）。
func TestPublishDecision_RequiresProblemAndEvidence(t *testing.T) {
	graph := newGraphWithActions(t)
	ctx := observedCtx(t, graph, "t1")
	_, err := graph.CreateNode(context.Background(), explorationgraph.Node{
		ID: "foreign", TaskID: "t2", Kind: core.KindResult, Content: []byte(`{}`),
	})
	require.NoError(t, err)

	cases := []struct {
		name     string
		decision Decision
		wantErr  string
	}{
		{"未知决策类型", Decision{Type: "bogus", Problem: ProblemStalled, EvidenceIDs: []string{"a-done"}, Reason: "r"}, "type must be reject_action or request_replan"},
		{"缺问题类别", Decision{Type: DecisionRequestReplan, EvidenceIDs: []string{"a-done"}, Reason: "本轮无需干预"}, "problem"},
		{"类别不属于该决策", Decision{Type: DecisionRequestReplan, Problem: ProblemDuplicate, EvidenceIDs: []string{"a-done"}, Reason: "r"}, "problem"},
		{"缺证据", Decision{Type: DecisionRequestReplan, Problem: ProblemStalled, Reason: "r"}, "evidence_ids"},
		{"证据不是本任务的节点", Decision{Type: DecisionRequestReplan, Problem: ProblemStalled, EvidenceIDs: []string{"foreign"}, Reason: "r"}, "foreign"},
		{"否决缺 action_id", Decision{Type: DecisionRejectAction, Problem: ProblemDuplicate, EvidenceIDs: []string{"a-done"}, Reason: "r"}, "action_id is required for reject_action"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			planner := &replanRecorder{}
			tool := NewPublishDecisionTool(graph, planner, "t1")
			out, err := tool.Execute(ctx, decisionArgs(t, c.decision))
			require.NoError(t, err)
			assert.Contains(t, out.Error, c.wantErr)
			assert.Empty(t, planner.reasons, "无效决策不得生效")
		})
	}
}

// reject_action 只能否决本任务的 action：其他任务的节点、本任务的非 action 节点都拒绝且不改状态。
func TestPublishDecision_RejectActionRefusesForeignOrNonAction(t *testing.T) {
	graph := newGraphWithActions(t)
	ctx := observedCtx(t, graph, "t1")
	stateOpen := explorationgraph.StateOpen
	mk := func(task string, kind core.NodeKind) string {
		id := "n-" + uuid.New().String()
		_, err := graph.CreateNode(context.Background(), explorationgraph.Node{ID: id, TaskID: task, Kind: kind, State: &stateOpen, Content: []byte(`{}`)})
		require.NoError(t, err)
		return id
	}
	foreign := mk("other-task", core.KindAction)
	objective := mk("t1", core.KindObjective)
	tool := NewPublishDecisionTool(graph, &replanRecorder{}, "t1")

	for _, id := range []string{foreign, objective} {
		res, err := tool.Execute(ctx, decisionArgs(t, Decision{
			Type: DecisionRejectAction, ActionID: id, Problem: ProblemOffGoal, EvidenceIDs: []string{"a-done"}, Reason: "x",
		}))
		require.NoError(t, err)
		assert.NotEmpty(t, res.Error, "应拒绝 %s", id)
		node, err := graph.GetNode(context.Background(), id)
		require.NoError(t, err)
		assert.Equal(t, explorationgraph.StateOpen, *node.State, "被拒节点状态不得改变")
	}
}
