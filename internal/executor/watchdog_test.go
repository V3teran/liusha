package executor

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/bus"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
)

// stuckExecutor 卡住不再有任何进展，直到 ctx 被取消。
type stuckExecutor struct{}

func (stuckExecutor) Execute(ctx context.Context, _ explorationgraph.Node) (Outcome, error) {
	<-ctx.Done()
	return OutcomeDone, ctx.Err()
}

func newWatchdogAgent(t *testing.T, exec ActionExecutor, activity *ActivityTracker) (*Agent, *explorationgraph.Store) {
	t.Helper()
	store := explorationgraph.NewMemoryStore()
	open := explorationgraph.StateOpen
	_, err := store.CreateNode(context.Background(), explorationgraph.Node{
		ID: "a1", TaskID: "t1", Kind: core.KindAction, State: &open,
		SourceType: explorationgraph.SourcePlanner, Content: []byte(`{"instruction":"扫描"}`),
	})
	require.NoError(t, err)
	a := NewAgent(AgentConfig{
		TaskID: "t1", AgentRunID: "run-1", Graph: store, Executor: exec,
		EventBus: bus.New(context.Background()), Logger: zerolog.Nop(),
		Activity: activity, StallTimeout: 150 * time.Millisecond, StallCheckInterval: 20 * time.Millisecond,
	})
	return a, store
}

// 动作长时间没有任何进展（无工具调用、无 LLM 步）即判停摆：取消执行并记失败原因，
// 不占着并发槽等到 run 超时。
func TestWatchdog_StalledActionFailsWithReason(t *testing.T) {
	activity := NewActivityTracker()
	a, store := newWatchdogAgent(t, stuckExecutor{}, activity)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.watchStalls(ctx)

	done := make(chan error, 1)
	go func() { done <- a.executeAction(ctx, mustNode(t, store, "a1")) }()
	select {
	case err := <-done:
		require.Error(t, err, "停摆是执行失败")
	case <-time.After(5 * time.Second):
		t.Fatal("停摆的动作未被看门狗结束")
	}

	n := mustNode(t, store, "a1")
	assert.Equal(t, explorationgraph.StateFailed, *n.State)
	require.NotNil(t, n.StateReason)
	assert.True(t, strings.Contains(*n.StateReason, "停摆"), "原因应说明停摆：%s", *n.StateReason)
}

// 持续有进展的长动作不会被误杀：判据是距最近一次进展的时长，不是动作已运行多久。
// 由测试直接驱动判定（不起看门狗循环、不靠真实计时），结果与机器负载无关。
func TestWatchdog_ActiveActionIsNotKilled(t *testing.T) {
	activity := NewActivityTracker()
	exec := newGatedExecutor("a1")
	a, store := newWatchdogAgent(t, exec, activity)

	done := make(chan error, 1)
	go func() { done <- a.executeAction(context.Background(), mustNode(t, store, "a1")) }()
	exec.waitStarted(t, "a1")

	// 动作已运行了 10 个停摆阈值之久，但刚有过进展。
	a.inFlightMu.Lock()
	f := a.inFlight["a1"]
	f.started = time.Now().Add(-10 * a.stallTimeout)
	a.inFlight["a1"] = f
	a.inFlightMu.Unlock()
	activity.Touch("a1")
	a.cancelStalled(time.Now())

	close(exec.gates["a1"])
	require.NoError(t, <-done)
	assert.Equal(t, explorationgraph.StateCompleted, *mustNode(t, store, "a1").State)
}
