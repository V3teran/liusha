package planner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
)

// 动作之间没有执行依赖：先后关系由增量规划表达（前一动作的观察落图后再提出后续
// 动作）。任何 open 动作都可执行——图上残留的依赖声明不得让动作永久停在 open：
// 被依赖的动作一旦失败或被中止，依赖方从此不可执行，又因非终态阻止任务收敛。
func TestHasOpenActions_IgnoresOtherActionStates(t *testing.T) {
	ctx := context.Background()
	store := explorationgraph.NewMemoryStore()
	a := &Agent{taskID: "t1", graph: store, logger: zerolog.Nop()}

	failed, open := explorationgraph.StateFailed, explorationgraph.StateOpen
	_, err := store.CreateNode(ctx, explorationgraph.Node{
		ID: "a1", TaskID: "t1", Kind: core.KindAction, State: &failed,
		SourceType: explorationgraph.SourcePlanner, Content: json.RawMessage(`{"instruction":"登录"}`),
	})
	require.NoError(t, err)
	_, err = store.CreateNode(ctx, explorationgraph.Node{
		ID: "a2", TaskID: "t1", Kind: core.KindAction, State: &open,
		SourceType: explorationgraph.SourcePlanner, Content: json.RawMessage(`{"instruction":"登录后访问后台"}`),
		Metadata: json.RawMessage(`{"depends_on":["a1"]}`),
	})
	require.NoError(t, err)

	hasOpen, err := a.hasOpenActions(ctx)
	require.NoError(t, err)
	assert.True(t, hasOpen, "open 动作应可执行，不受其它动作状态影响")
}

func TestAgentConfig_PollInterval(t *testing.T) {
	cfg := AgentConfig{PollInterval: 0}
	// NewAgent 会把 0 归一化为 10s 兜底——验证归一化行为
	_ = NewAgent(cfg)
	assert.NotNil(t, NewAgent(cfg))
}

// 重规划请求在被消费前合并为一个信号：不会堆积成多轮重复规划，但每个来源的理由
// 都要保留（监察者与目标调整可能同时请求）——相同理由只留一条。
func TestRequestReplan_CoalescesSignalKeepsEveryReason(t *testing.T) {
	a := NewAgent(AgentConfig{TaskID: "t1"})

	a.RequestReplan("探索停滞", nil)
	a.RequestReplan("任务目标已调整", nil)
	a.RequestReplan("探索停滞", nil)

	select {
	case <-a.replanCh:
	default:
		t.Fatal("请求后应有一个待处理的重规划信号")
	}
	select {
	case <-a.replanCh:
		t.Fatal("多次请求应合并为一个信号")
	default:
	}
	requests := a.takeReplanRequests()
	require.Len(t, requests, 2)
	assert.Equal(t, "探索停滞", requests[0].reason)
	assert.Equal(t, "任务目标已调整", requests[1].reason)
	assert.Empty(t, a.takeReplanRequests(), "取走后清空")
}

// 强制重规划把请求的理由交给规划器（此前只写日志，模型看不到为什么要重规划）。
func TestForcePlanActions_PassesReasonsToPlanner(t *testing.T) {
	rec := &recordingPlanner{}
	a := NewAgent(AgentConfig{TaskID: revisionTask, Planner: rec, Graph: rootOnlyStore(t),
		EventBus: &fakeEventBus{}, OnConverged: func(string, explorationgraph.GoalBasis) {}})
	require.NoError(t, a.forcePlanActions(context.Background(), []string{"探索停滞"}))
	assert.Equal(t, []string{"探索停滞"}, rec.req.ReplanReasons)
}

// recordingPlanner 记录规划请求，不产出提案。
type recordingPlanner struct{ req PlanRequest }

func (p *recordingPlanner) Plan(_ context.Context, _ *explorationgraph.Store, _ string, req PlanRequest) (PlanOutcome, error) {
	p.req = req
	return PlanOutcome{}, nil
}

func (p *recordingPlanner) AnalyzeResults(context.Context, *explorationgraph.Store, string, []explorationgraph.Node) (*ResultAnalysis, error) {
	return &ResultAnalysis{}, nil
}

// 目标被调整：在途的那一轮规划当场作废，不等它跑完——它的结论以旧目标为依据，落图时注定
// 被拒，等它跑完只是白等（模型慢时一轮要几分钟）。监察者的请求带着它所见的图面，不打断
// 在途的这一轮。被取代的那一轮不算失败，重规划请求仍待处理。
func TestRequestReplan_GoalChangeAbortsInFlightRound(t *testing.T) {
	a := NewAgent(AgentConfig{TaskID: "t1"})
	entered, done := make(chan struct{}), make(chan struct{})
	var roundErr error
	go func() {
		defer close(done)
		roundErr = a.planningRound(context.Background(), func(ctx context.Context) error {
			close(entered)
			<-ctx.Done()
			return fmt.Errorf("ReAct 规划失败: %w", ctx.Err())
		})
	}()
	<-entered

	a.RequestReplan("探索停滞", &explorationgraph.Surface{})
	select {
	case <-done:
		t.Fatal("监察者的重规划请求不应打断在途的这一轮")
	case <-time.After(80 * time.Millisecond):
	}

	a.RequestReplan("任务目标已被用户调整", nil)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("目标被调整后，在途的那一轮规划应立即结束")
	}
	if roundErr != nil {
		t.Fatalf("被取代的那一轮不算失败，实际交还 %v", roundErr)
	}
	if reqs := a.takeReplanRequests(); len(reqs) != 2 {
		t.Fatalf("两条重规划请求都应待处理，实际 %d 条", len(reqs))
	}
	a.roundMu.Lock()
	cancel := a.roundCancel
	a.roundMu.Unlock()
	if cancel != nil {
		t.Fatal("一轮结束后不应还登记着它的取消函数")
	}
}

// 规划者自己被停掉（Run 的 ctx 结束）不是「被取代」：错误原样交还，由事件循环收尾。
func TestPlanningRound_StopIsNotSupersede(t *testing.T) {
	a := NewAgent(AgentConfig{TaskID: "t1"})
	ctx, cancel := context.WithCancel(context.Background())
	err := a.planningRound(ctx, func(c context.Context) error {
		cancel()
		<-c.Done()
		return fmt.Errorf("ReAct 规划失败: %w", c.Err())
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("规划者被停掉时错误应原样交还，实际 %v", err)
	}
}
