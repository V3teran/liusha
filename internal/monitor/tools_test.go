package monitor

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/bus"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/monitor/metrics"
)

// monitor 的两个工具是监察决策的出入口：
// GetGlobalStateTool 汇总探索图态势；PublishDecisionTool 的 kill_action
// 直接把 action 置 aborted（executor 认领前检查 state，即真实生效路径）。

func newGraphWithActions(t *testing.T) *explorationgraph.Store {
	t.Helper()
	graph := explorationgraph.NewMemoryStore()
	ctx := context.Background()

	obj, err := graph.CreateNode(ctx, explorationgraph.Node{
		ID: "obj-1", TaskID: "t1", Kind: core.KindObjective,
		Content: []byte(`{"description":"测试目标"}`),
	})
	require.NoError(t, err)

	state := explorationgraph.StateOpen
	for _, id := range []string{"a-done", "a-open", "a-running"} {
		st := state
		content := []byte(`{"instruction":"do something"}`)
		if id == "a-done" {
			done := explorationgraph.StateDone
			st = done
			content = []byte(`{"instruction":"done thing"}`)
		}
		if _, err := graph.CreateNode(ctx, explorationgraph.Node{
			ID: id, TaskID: "t1", Kind: core.KindAction,
			Content: content, State: &st,
		}); err != nil {
			t.Fatalf("create action %s: %v", id, err)
		}
	}
	_ = obj
	return graph
}

func TestGetGlobalStateTool_SumsExplorationState(t *testing.T) {
	graph := newGraphWithActions(t)
	ctx := context.Background()

	// 创建 metrics collector
	logger := zerolog.New(zerolog.NewTestWriter(t))
	metricsCollector := metrics.NewMetricsCollector(graph, "t1", logger)

	tool := NewGetGlobalStateTool(graph, "t1", metricsCollector)
	out, err := tool.Execute(ctx, []byte(`{}`))
	require.NoError(t, err)
	require.Empty(t, out.Error)

	var state struct {
		Objective explorationgraph.ObjectiveNode `json:"objective"`
		Actions   []explorationgraph.Node        `json:"actions"`
		Findings  []explorationgraph.Node        `json:"findings"`
	}
	require.NoError(t, json.Unmarshal([]byte(out.Output), &state))
	assert.Len(t, state.Actions, 3, "三个 action 全量返回")
	assert.NotNil(t, state.Objective.ID, "objective 应存在")
}

func TestPublishDecisionTool_KillAction_AppliesStateChange(t *testing.T) {
	graph := newGraphWithActions(t)
	ctx := context.Background()
	eventBus := bus.New(ctx)
	tool := NewPublishDecisionTool(graph, eventBus, "t1")

	out, err := tool.Execute(ctx, []byte(
		`{"type":"kill_action","action_id":"a-open","reason":"监察发现无效循环"}`))
	require.NoError(t, err)
	require.Empty(t, out.Error)

	// kill 的生效路径是探索图状态变更：aborted 后 executor 不再认领
	node, err := graph.GetNode(ctx, "a-open")
	require.NoError(t, err)
	require.NotNil(t, node.State)
	assert.Equal(t, "aborted", string(*node.State))
}

func TestPublishDecisionTool_RequestReplan(t *testing.T) {
	graph := newGraphWithActions(t)
	ctx := context.Background()
	eventBus := bus.New(ctx)
	tool := NewPublishDecisionTool(graph, eventBus, "t1")

	out, err := tool.Execute(ctx, json.RawMessage(`{"type":"request_replan","reason":"当前方向停滞"}`))
	require.NoError(t, err)
	require.Empty(t, out.Error)
}

func TestPublishDecisionTool_Validation(t *testing.T) {
	graph := newGraphWithActions(t)
	ctx := context.Background()
	eventBus := bus.New(ctx)
	tool := NewPublishDecisionTool(graph, eventBus, "t1")

	cases := []struct {
		name, args, wantErr string
	}{
		{"未知决策类型", `{"type":"bogus","reason":"r"}`, "type must be kill_action or request_replan"},
		{"kill_action 缺 action_id", `{"type":"kill_action","reason":"r"}`, "action_id is required for kill_action"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := tool.Execute(ctx, json.RawMessage(c.args))
			require.NoError(t, err)
			assert.Contains(t, out.Error, c.wantErr)
		})
	}
}

// running_actions 时长视图是 monitor kill 决策的变量源（此前只给原始时间戳，
// LLM 无从计算时长，20 分钟 kill 职责空转——e2e 实测 action 跑 30+ 分钟无人杀）。
func TestRunningActionViews(t *testing.T) {
	now := time.Now()
	running := explorationgraph.StateRunning
	done := explorationgraph.StateDone
	mk := func(id string, st explorationgraph.State, ageMin float64) explorationgraph.Node {
		return explorationgraph.Node{
			ID: id, Kind: core.KindAction, State: &st,
			Content:   json.RawMessage(`{"instruction":"做某事"}`),
			UpdatedAt: now.Add(-time.Duration(ageMin * float64(time.Minute))),
		}
	}
	actions := []explorationgraph.Node{
		mk("a-running-30m", running, 30),
		mk("b-running-2m", running, 2),
		mk("c-done", done, 99),      // 非 running 不进视图
		mk("d-future", running, -5), // 时钟偏移防御：负时长归零
	}

	views := runningActionViews(actions, now)
	if len(views) != 3 {
		t.Fatalf("应只含 3 个 running 动作, got %d", len(views))
	}
	if views[0].ID != "a-running-30m" || views[0].RunningMinutes < 29.9 || views[0].RunningMinutes > 30.1 {
		t.Fatalf("30 分钟动作时长应正确计算, got %+v", views[0])
	}
	if views[1].RunningMinutes < 1.9 || views[1].RunningMinutes > 2.1 {
		t.Fatalf("2 分钟动作时长应正确计算, got %+v", views[1])
	}
	if views[2].RunningMinutes != 0 {
		t.Fatalf("时钟偏移应归零, got %+v", views[2])
	}
	if views[0].Instruction != "做某事" {
		t.Fatalf("视图应携带 instruction 供决策上下文, got %+v", views[0])
	}
}
