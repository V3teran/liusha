package cognition

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/controlplane"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
)

const objTask = "task-objectives"

func seedObjective(t *testing.T, store *explorationgraph.Store, id string, source explorationgraph.SourceType, state explorationgraph.State, offset time.Duration) {
	t.Helper()
	_, err := store.CreateNode(context.Background(), explorationgraph.Node{
		ID: id, TaskID: objTask, Kind: core.KindObjective, State: &state, SourceType: source,
		Content: []byte(`{"description":"` + id + `"}`), CreatedAt: time.Unix(1700000000, 0).Add(offset),
	})
	require.NoError(t, err)
}

func objectiveState(t *testing.T, store *explorationgraph.Store, id string) explorationgraph.State {
	t.Helper()
	n, err := store.GetNode(context.Background(), id)
	require.NoError(t, err)
	return *n.State
}

// 任务收口：根目标随任务结局；派生目标的 completed 只属于有证据的达成，
// 正常结束时仍 open 的派生目标落 exhausted；已终态的目标不动。
func TestCloseObjectives_RootFollowsTaskDerivedNeverFakesCompletion(t *testing.T) {
	cases := []struct {
		stopWhy              string
		wantRoot, wantDerive explorationgraph.State
	}{
		{"task_converged", explorationgraph.StateCompleted, explorationgraph.StateExhausted},
		{"max_steps_reached", explorationgraph.StateCompleted, explorationgraph.StateExhausted},
		{StopReasonTerminated, explorationgraph.StateAborted, explorationgraph.StateAborted},
		{StopReasonAgentFailed, explorationgraph.StateFailed, explorationgraph.StateFailed},
	}
	for _, tc := range cases {
		t.Run(tc.stopWhy, func(t *testing.T) {
			store := explorationgraph.NewMemoryStore()
			seedObjective(t, store, "root", explorationgraph.SourceUser, explorationgraph.StateOpen, 0)
			seedObjective(t, store, "live", explorationgraph.SourcePlanner, explorationgraph.StateOpen, time.Second)
			seedObjective(t, store, "achieved", explorationgraph.SourcePlanner, explorationgraph.StateCompleted, 2*time.Second)
			d := &Deps{Graph: store, Logger: zerolog.Nop()}

			d.closeObjectives(context.Background(), objTask, tc.stopWhy)

			assert.Equal(t, tc.wantRoot, objectiveState(t, store, "root"))
			assert.Equal(t, tc.wantDerive, objectiveState(t, store, "live"))
			assert.Equal(t, explorationgraph.StateCompleted, objectiveState(t, store, "achieved"))
		})
	}
}

func TestCloseObjectives_PauseKeepsEverythingOpen(t *testing.T) {
	store := explorationgraph.NewMemoryStore()
	seedObjective(t, store, "root", explorationgraph.SourceUser, explorationgraph.StateOpen, 0)
	seedObjective(t, store, "live", explorationgraph.SourcePlanner, explorationgraph.StateOpen, time.Second)

	(&Deps{Graph: store, Logger: zerolog.Nop()}).closeObjectives(context.Background(), objTask, StopReasonPaused)

	assert.Equal(t, explorationgraph.StateOpen, objectiveState(t, store, "root"))
	assert.Equal(t, explorationgraph.StateOpen, objectiveState(t, store, "live"))
}

// 人工注入的动作与规划产出的动作同样归属根目标，不是孤立节点。
func TestInjectAction_HangsOnRootObjective(t *testing.T) {
	store := explorationgraph.NewMemoryStore()
	seedObjective(t, store, "root", explorationgraph.SourceUser, explorationgraph.StateOpen, 0)
	seedObjective(t, store, "live", explorationgraph.SourcePlanner, explorationgraph.StateOpen, time.Second)
	ctx := context.Background()

	require.NoError(t, injectAction(ctx, objTask, store, controlplane.InjectActionPayload{Kind: "testing", Reason: "人工复测"}, zerolog.Nop()))

	actions, err := store.ListNodesByKind(ctx, objTask, core.KindAction)
	require.NoError(t, err)
	require.Len(t, actions, 1)
	edges, err := store.ListEdgesByTask(ctx, objTask)
	require.NoError(t, err)
	require.Len(t, edges, 1)
	assert.Equal(t, explorationgraph.Edge{
		TaskID: objTask, SrcID: "root", Rel: explorationgraph.RelGenerates, DstID: actions[0].ID,
	}, explorationgraph.Edge{TaskID: edges[0].TaskID, SrcID: edges[0].SrcID, Rel: edges[0].Rel, DstID: edges[0].DstID})
}

func TestInjectAction_RequiresRootObjective(t *testing.T) {
	store := explorationgraph.NewMemoryStore()
	err := injectAction(context.Background(), objTask, store, controlplane.InjectActionPayload{Kind: "testing", Reason: "x"}, zerolog.Nop())
	assert.Error(t, err)
}
