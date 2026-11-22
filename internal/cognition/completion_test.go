package cognition

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
)

// CompletionDetector 是任务终止判定的唯一入口：人工中止（含暂停）> 收敛 > 动作预算。
// 动作预算与报告统计都从图上计数，不依赖事件总线。

const detTask = "t1"

func newDetector(t *testing.T, maxActions int) (*CompletionDetector, *explorationgraph.Store) {
	t.Helper()
	store := explorationgraph.NewMemoryStore()
	d := NewCompletionDetector(Config{
		TaskID:        detTask,
		Graph:         store,
		Logger:        zerolog.Nop(),
		MaxActions:    maxActions,
		CheckInterval: 10 * time.Millisecond,
	})
	return d, store
}

// seedAction 建一个动作；claimed 为真时模拟已被执行者认领（执行过）。
func seedAction(t *testing.T, store *explorationgraph.Store, id string, claimed bool) {
	t.Helper()
	ctx := context.Background()
	open := explorationgraph.StateOpen
	_, err := store.CreateNode(ctx, explorationgraph.Node{
		ID: id, TaskID: detTask, Kind: core.KindAction, State: &open,
		SourceType: explorationgraph.SourcePlanner, Content: []byte(`{"instruction":"x"}`),
	})
	require.NoError(t, err)
	if claimed {
		ok, err := store.ClaimAction(ctx, detTask, id, "run-1")
		require.NoError(t, err)
		require.True(t, ok)
	}
}

// 动作预算按「执行过的动作」计数（被认领过即算），未执行的 open 动作不占预算。
func TestCompletionDetector_ActionBudgetCountsExecutedActions(t *testing.T) {
	d, store := newDetector(t, 2)
	seedAction(t, store, "a1", true)
	seedAction(t, store, "a2", false)

	_, done := d.checkCompletion()
	assert.False(t, done, "执行过 1 个动作，未到预算 2")

	seedAction(t, store, "a3", true)
	res, done := d.checkCompletion()
	require.True(t, done, "执行过 2 个动作，达到预算")
	assert.Equal(t, StopReasonActionBudget, res.StopReason)
}

// 报告统计从图上读：执行过的动作、坐实结果、提出的假设。
func TestCompletionDetector_ReportStatsFromGraph(t *testing.T) {
	d, store := newDetector(t, 0)
	ctx := context.Background()
	seedAction(t, store, "a1", true)
	seedAction(t, store, "a2", false)
	pending := explorationgraph.VerificationPending
	_, err := store.CreateNode(ctx, explorationgraph.Node{ID: "h1", TaskID: detTask, Kind: core.KindObservation,
		VerificationStatus: &pending, Content: []byte(`{"statement":"h"}`)})
	require.NoError(t, err)
	_, err = store.CreateNode(ctx, explorationgraph.Node{ID: "f1", TaskID: detTask, Kind: core.KindObservation,
		Content: []byte(`{"statement":"fact"}`)})
	require.NoError(t, err)
	_, err = store.CreateNode(ctx, explorationgraph.Node{ID: "r1", TaskID: detTask, Kind: core.KindResult,
		Content: []byte(`{"statement":"r"}`)})
	require.NoError(t, err)

	got := d.makeResult(StopReasonConverged)
	assert.Equal(t, 1, got.Actions, "只计执行过的动作")
	assert.Equal(t, 1, got.Results)
}

func TestCompletionDetector_AbortWins(t *testing.T) {
	d, _ := newDetector(t, 0)

	d.Abort("control-plane: terminate")
	res, done := d.checkCompletion()
	require.True(t, done, "人工中止应立即判定完成")
	assert.Equal(t, "control-plane: terminate", res.StopReason)
}

// 暂停退出复用中止通道，且优先于动作预算：停止原因必须原样透传给 runner 分流。
func TestCompletionDetector_AbortCarriesPauseReason(t *testing.T) {
	d, store := newDetector(t, 1)
	seedAction(t, store, "a1", true)
	d.Abort(StopReasonPaused)

	res, done := d.checkCompletion()
	require.True(t, done)
	assert.Equal(t, StopReasonPaused, res.StopReason)
}

// agent 致命失败（如模型配置错误）必须终结检测循环并携带错误——否则任务永不收敛，
// 停在 active。
func TestCompletionDetector_FailStopsWithError(t *testing.T) {
	d, _ := newDetector(t, 0)
	cause := errors.New("planner agent: http 401")
	go d.Fail(cause)

	done := make(chan Result, 1)
	go func() { done <- d.Start(context.Background()) }()
	select {
	case res := <-done:
		if !errors.Is(res.Err, cause) {
			t.Fatalf("Result.Err 应携带致命错误，得 %v", res.Err)
		}
		if res.StopReason != StopReasonAgentFailed {
			t.Fatalf("StopReason 应为 %q，得 %q", StopReasonAgentFailed, res.StopReason)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Fail 后检测循环未退出")
	}
}

// 收敛由规划者直接通知，检测器立即结束（不等定时兜底检查）。
func TestCompletionDetector_ConvergeStopsImmediately(t *testing.T) {
	d := NewCompletionDetector(Config{
		TaskID: detTask, Graph: explorationgraph.NewMemoryStore(), Logger: zerolog.Nop(), CheckInterval: time.Hour,
	})
	done := make(chan Result, 1)
	go func() { done <- d.Start(context.Background()) }()

	basis := explorationgraph.GoalBasis{ObjectiveID: "root", Version: 3}
	d.Converge("planner: 无新方向且图上无未完结工作", basis)

	select {
	case r := <-done:
		assert.Equal(t, StopReasonConverged, r.StopReason)
		assert.Equal(t, basis, r.GoalBasis, "收敛带出它所依据的目标，任务落 completed 时以它为前置")
	case <-time.After(2 * time.Second):
		t.Fatal("收敛后检测器未结束")
	}
}

// ctx 取消（run 被打断）时检测循环结束并如实报告。
func TestCompletionDetector_ContextCancelled(t *testing.T) {
	d, _ := newDetector(t, 0)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan Result, 1)
	go func() { done <- d.Start(ctx) }()
	cancel()
	select {
	case r := <-done:
		assert.Equal(t, StopReasonCancelled, r.StopReason)
	case <-time.After(2 * time.Second):
		t.Fatal("ctx 取消后检测器未结束")
	}
}

// 生命周期：agent 非取消性退出时上报 onFatal；正常停止（ctx 取消）不上报。
func TestAgentLifecycle_ReportsUnexpectedExit(t *testing.T) {
	var got []string
	var mu sync.Mutex
	report := func(name string, err error) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, name+":"+err.Error())
	}
	l := newAgentLifecycle(context.Background(), zerolog.Nop(), []agentSpec{
		{name: "planner", start: func(context.Context) error { return errors.New("http 401") }},
		{name: "executor", start: func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }},
	}, report)
	l.Start()
	deadline := time.Now().Add(time.Second)
	for {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	l.Stop()
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0] != "planner:http 401" {
		t.Fatalf("应只上报 planner 的异常退出，得 %v", got)
	}
}

// Abort 与后台兜底检查并发时，检查一旦观察到「已中止」就必须同时拿到调用方传入的
// 原因，绝不能回落到兜底值——否则用户的中止原因被吞。紧循环轮询放大竞态窗口。
func TestCompletionDetector_AbortReasonNeverLostToConcurrentCheck(t *testing.T) {
	const reason = "user requested termination"
	for i := 0; i < 20000; i++ {
		d := NewCompletionDetector(Config{TaskID: "t", Logger: zerolog.Nop()})
		go d.Abort(reason)
		for {
			if res, done := d.checkCompletion(); done {
				if res.StopReason != reason {
					t.Fatalf("第 %d 轮：中止原因丢失，got %q want %q", i, res.StopReason, reason)
				}
				break
			}
		}
	}
}
