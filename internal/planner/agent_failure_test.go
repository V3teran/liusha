package planner

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/bus"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/framework/runtime"
)

// 规划失败的处置：输入超过单条消息的上限是确定性的，立刻终止本轮运行；其余失败进入冷却，
// 冷却随连续失败的次数增长，成功一次清零。
func TestPlanFailure_InputTooLongIsFatal(t *testing.T) {
	a := &Agent{logger: zerolog.Nop()}
	err := fmt.Errorf("plan: ReAct 规划失败: LLM 调用失败: message modifier chain failed: %w", runtime.ErrMessageTooLong)

	got := a.planFailure(err, "定期规划")

	require.ErrorIs(t, got, runtime.ErrMessageTooLong, "原样交还，由认知循环终结本轮运行")
	assert.False(t, a.coolingDown(), "致命失败不进入冷却")
}

func TestPlanFailure_OtherFailuresCoolDownWithBackoff(t *testing.T) {
	a := &Agent{logger: zerolog.Nop()}
	before := time.Now()

	require.NoError(t, a.planFailure(errors.New("图库连接中断"), "定期规划"))
	assert.True(t, a.coolingDown())
	require.WithinDuration(t, before.Add(5*time.Second), a.planRetryAt, time.Second)

	for i := 0; i < 6; i++ {
		require.NoError(t, a.planFailure(errors.New("图库连接中断"), "定期规划"))
	}
	require.WithinDuration(t, time.Now().Add(2*time.Minute), a.planRetryAt, time.Second, "冷却封顶 2 分钟")

	require.NoError(t, a.planFailure(nil, "定期规划"))
	assert.False(t, a.coolingDown(), "成功一次即清零")
	assert.Zero(t, a.planFailures)
}

func TestPlanBackoff(t *testing.T) {
	for failures, want := range map[int]time.Duration{0: 5 * time.Second, 1: 5 * time.Second, 2: 10 * time.Second, 3: 20 * time.Second, 4: 40 * time.Second, 5: 80 * time.Second, 6: 2 * time.Minute, 50: 2 * time.Minute} {
		assert.Equal(t, want, planBackoff(0, failures), "连续失败 %d 次", failures)
	}
	assert.Equal(t, time.Millisecond, planBackoff(time.Millisecond, 1))
	assert.Equal(t, 24*time.Millisecond, planBackoff(time.Millisecond, 9), "封顶随起点缩放")
}

// 非瞬时的失败连续到上限：带着最后一次的原因终止本轮运行，不再只带着冷却撞下去。
func TestPlanFailure_PersistentHardFailuresEndTheRun(t *testing.T) {
	a := &Agent{logger: zerolog.Nop()}
	for i := 1; i < persistentPlanFailures; i++ {
		require.NoError(t, a.planFailure(errors.New("图库连接中断"), "定期规划"), "第 %d 次不应终止", i)
	}
	err := a.planFailure(errors.New("图库连接中断"), "定期规划")
	require.Error(t, err)
	assert.ErrorContains(t, err, "图库连接中断")
	assert.Contains(t, err.Error(), fmt.Sprintf("规划连续 %d 次失败", persistentPlanFailures))
}

// 模型服务的瞬时故障（额度窗口、网络、超时）不计上限，还会把连续非瞬时的计数清零；被停掉的
// 那一轮不是规划的失败。
func TestPlanFailure_TransientAndCanceledFailuresDoNotCount(t *testing.T) {
	a := &Agent{logger: zerolog.Nop()}
	for i := 1; i < persistentPlanFailures; i++ {
		require.NoError(t, a.planFailure(errors.New("图库连接中断"), "定期规划"), "第 %d 次不应终止", i)
	}
	transient := fmt.Errorf("plan: 重试 3 次后仍失败: %w", context.DeadlineExceeded)
	for i := 0; i < 3*persistentPlanFailures; i++ {
		require.NoError(t, a.planFailure(transient, "定期规划"), "瞬时故障再多次也不终止")
	}
	require.NoError(t, a.planFailure(errors.New("图库连接中断"), "定期规划"), "瞬时故障之后计数已清零")
	require.NoError(t, a.planFailure(fmt.Errorf("ReAct 规划失败: %w", context.Canceled), "定期规划"), "被取消的一轮不算失败")
	for i := 2; i < persistentPlanFailures; i++ {
		require.NoError(t, a.planFailure(errors.New("图库连接中断"), "定期规划"))
	}
	assert.Error(t, a.planFailure(errors.New("图库连接中断"), "定期规划"))
}

// 事件循环层面：规划一直非瞬时地失败，Run 在冷却走完之后带着原因退出，而不是被恢复监控
// 当成僵尸收场。
func TestRun_PersistentHardFailureEndsTheRunWithTheCause(t *testing.T) {
	planner := &countingPlanner{err: errors.New("图库连接中断")}
	a := runnableAgent(t, planner)
	a.planBackoffBase = time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := a.Run(ctx)

	require.Error(t, err)
	assert.ErrorContains(t, err, "图库连接中断")
	assert.NotErrorIs(t, err, context.DeadlineExceeded, "是规划者自己退出，不是测试的超时")
	assert.EqualValues(t, persistentPlanFailures, planner.calls)
}

// countingPlanner 每次规划都返回同一个错误，并数自己被调用了几次。
type countingPlanner struct {
	calls int32
	err   error
}

func (p *countingPlanner) Plan(context.Context, *explorationgraph.Store, string, PlanRequest) (PlanOutcome, error) {
	p.calls++
	return PlanOutcome{}, p.err
}

func (p *countingPlanner) AnalyzeResults(context.Context, *explorationgraph.Store, string, []explorationgraph.Node) (*ResultAnalysis, error) {
	return &ResultAnalysis{}, nil
}

func runnableAgent(t *testing.T, planner GraphPlanner) *Agent {
	t.Helper()
	store := explorationgraph.NewMemoryStore()
	_, err := store.CreateNode(context.Background(), revisionNode("root", core.KindObjective, explorationgraph.SourceUser, openState(), 0))
	require.NoError(t, err)
	return &Agent{
		// 真实的事件总线：Run 要订阅它（测试替身的 SubscribeTask 返回空）
		taskID: revisionTask, graph: store, planner: planner, eventBus: bus.New(context.Background()), logger: zerolog.Nop(),
		pollInterval: 5 * time.Millisecond, stopCh: make(chan struct{}), replanCh: make(chan struct{}, 1),
	}
}

// 规划持续失败时不再每隔几秒撞一次：第一次失败之后进入冷却，轮询与事件都不再触发规划。
// 曾有一轮规划在两个半小时里失败了 1342 次。
func TestRun_PersistentPlanningFailureCoolsDownInsteadOfHamming(t *testing.T) {
	planner := &countingPlanner{err: errors.New("图库连接中断")}
	a := runnableAgent(t, planner)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, cancel)

	_ = a.Run(ctx)

	assert.EqualValues(t, 1, planner.calls, "第一次失败之后是 5 秒冷却，300 毫秒内不应再撞")
}

// 输入超长：Run 以那个错误退出，而不是带着一个永远排不出动作的规划者继续空转。
func TestRun_InputTooLongEndsTheRun(t *testing.T) {
	planner := &countingPlanner{err: fmt.Errorf("message 1 exceeds max length: 103778 > 100000: %w", runtime.ErrMessageTooLong)}
	a := runnableAgent(t, planner)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := a.Run(ctx)

	require.ErrorIs(t, err, runtime.ErrMessageTooLong)
	assert.EqualValues(t, 1, planner.calls)
}
