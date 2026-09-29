package monitor

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
)

// monitor 的两个工具是监察决策的出入口：
// GetGlobalStateTool 汇总探索图态势；PublishDecisionTool 的 kill_action
// 直接把 action 置 aborted（executor 认领前检查 state，即真实生效路径）。

func newWorldWithActions(t *testing.T) *explorationgraph.Store {
	t.Helper()
	world := explorationgraph.NewMemoryStore()
	ctx := context.Background()

	obj, err := world.CreateNode(ctx, explorationgraph.Node{
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
		if _, err := world.CreateNode(ctx, explorationgraph.Node{
			ID: id, TaskID: "t1", Kind: core.KindAction,
			Content: content, State: &st,
		}); err != nil {
			t.Fatalf("create action %s: %v", id, err)
		}
	}
	_ = obj
	return world
}

func TestGetGlobalStateTool_SumsExplorationState(t *testing.T) {
	world := newWorldWithActions(t)
	ctx := context.Background()

	tool := NewGetGlobalStateTool(world, "t1")
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
	world := newWorldWithActions(t)
	ctx := context.Background()
	tool := NewPublishDecisionTool(world, "t1")

	out, err := tool.Execute(ctx, []byte(
		`{"type":"kill_action","action_id":"a-open","reason":"监察发现无效循环"}`))
	require.NoError(t, err)
	require.Empty(t, out.Error)

	// kill 的生效路径是探索图状态变更：aborted 后 executor 不再认领
	node, err := world.GetNode(ctx, "a-open")
	require.NoError(t, err)
	require.NotNil(t, node.State)
	assert.Equal(t, "aborted", string(*node.State))
}

func TestPublishDecisionTool_RequestReplan(t *testing.T) {
	world := newWorldWithActions(t)
	ctx := context.Background()
	tool := NewPublishDecisionTool(world, "t1")

	out, err := tool.Execute(ctx, json.RawMessage(`{"type":"request_replan","reason":"当前方向停滞"}`))
	require.NoError(t, err)
	require.Empty(t, out.Error)
}

func TestPublishDecisionTool_Validation(t *testing.T) {
	world := newWorldWithActions(t)
	ctx := context.Background()
	tool := NewPublishDecisionTool(world, "t1")

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
