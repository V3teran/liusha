package executor

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/bus"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
)

// revokedExecutor 执行到一半时动作被收回：像执行引擎的持有侦测一样读图，发现不再持有
// 即以 ErrActionRevoked 退出（run 本身仍存活）。
type revokedExecutor struct {
	store   *explorationgraph.Store
	started chan struct{}
}

func (r revokedExecutor) Execute(ctx context.Context, action explorationgraph.Node) (Outcome, error) {
	close(r.started)
	for {
		n, err := r.store.GetNode(ctx, action.ID)
		if err != nil {
			return OutcomeDone, err
		}
		if *n.State != explorationgraph.StateRunning {
			return OutcomeDone, fmt.Errorf("%w: %s", ErrActionRevoked, *n.State)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// 动作在执行途中被否决：执行方放弃本次结果、不算执行失败，否决状态与理由原样保留。
func TestExecuteAction_RevokedMidwayKeepsExternalDecision(t *testing.T) {
	store := explorationgraph.NewMemoryStore()
	open := explorationgraph.StateOpen
	_, err := store.CreateNode(context.Background(), explorationgraph.Node{
		ID: "a1", TaskID: "t1", Kind: core.KindAction, State: &open,
		SourceType: explorationgraph.SourcePlanner, Content: []byte(`{"instruction":"扫描 C 段"}`),
	})
	require.NoError(t, err)

	exec := revokedExecutor{store: store, started: make(chan struct{})}
	a := NewAgent(AgentConfig{
		TaskID: "t1", AgentRunID: "run-1", Graph: store, Executor: exec,
		EventBus: bus.New(context.Background()), Logger: zerolog.Nop(),
	})

	done := make(chan error, 1)
	go func() { done <- a.executeAction(context.Background(), mustNode(t, store, "a1")) }()
	<-exec.started
	ok, err := store.RejectAction(context.Background(), "t1", "a1", explorationgraph.SourceUser, explorationgraph.RejectStep, "超出授权范围")
	require.NoError(t, err)
	require.True(t, ok)

	select {
	case err := <-done:
		assert.NoError(t, err, "被收回不是执行失败")
	case <-time.After(5 * time.Second):
		t.Fatal("executeAction 未在动作被收回后返回")
	}
	n := mustNode(t, store, "a1")
	assert.Equal(t, explorationgraph.StateRejected, *n.State, "否决不得被执行方的结果覆盖")
	assert.Equal(t, "超出授权范围", *n.StateReason)
}
