package executor

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/bus"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
)

// blockingExecutor 一直执行到 ctx 取消，模拟 run 被中断时在途的动作。
type blockingExecutor struct{ started chan struct{} }

func (b blockingExecutor) Execute(ctx context.Context, _ explorationgraph.Node) (Outcome, error) {
	close(b.started)
	<-ctx.Done()
	return OutcomeDone, ctx.Err()
}

// run 被中断（暂停、停机、run 超时）时在途动作没有做完：退回 open 等下一个 run
// 重新执行，不得记成失败——规划者会把失败当作此路不通。
func TestExecuteAction_RunInterruptionReturnsActionToOpen(t *testing.T) {
	store := explorationgraph.NewMemoryStore()
	open := explorationgraph.StateOpen
	_, err := store.CreateNode(context.Background(), explorationgraph.Node{
		ID: "a1", TaskID: "t1", Kind: core.KindAction, State: &open,
		SourceType: explorationgraph.SourcePlanner, Content: []byte(`{"instruction":"扫描"}`),
	})
	require.NoError(t, err)

	exec := blockingExecutor{started: make(chan struct{})}
	a := NewAgent(AgentConfig{
		TaskID: "t1", AgentRunID: "run-1", Graph: store, Executor: exec,
		EventBus: bus.New(context.Background()), Logger: zerolog.Nop(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.executeAction(ctx, mustNode(t, store, "a1")) }()
	<-exec.started
	cancel()
	select {
	case err := <-done:
		assert.NoError(t, err, "中断不是执行错误")
	case <-time.After(5 * time.Second):
		t.Fatal("executeAction 未在 run 中断后返回")
	}

	n := mustNode(t, store, "a1")
	assert.Equal(t, explorationgraph.StateOpen, *n.State, "中断的动作应退回 open")
	assert.Nil(t, n.BlockedReason, "中断不是失败，不记失败原因")
}

func mustNode(t *testing.T, store *explorationgraph.Store, id string) explorationgraph.Node {
	t.Helper()
	n, err := store.GetNode(context.Background(), id)
	require.NoError(t, err)
	return *n
}
