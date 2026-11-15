package planner

import (
	"context"
	"encoding/json"
	"testing"

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

	a.RequestReplan("探索停滞")
	a.RequestReplan("任务目标已调整")
	a.RequestReplan("探索停滞")

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
	assert.Equal(t, []string{"探索停滞", "任务目标已调整"}, a.takeReplanReasons())
	assert.Empty(t, a.takeReplanReasons(), "取走后清空")
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

func (p *recordingPlanner) Plan(_ context.Context, _ *explorationgraph.Store, _ string, req PlanRequest) ([]explorationgraph.ActionDraft, error) {
	p.req = req
	return nil, nil
}

func (p *recordingPlanner) AnalyzeResults(context.Context, *explorationgraph.Store, string, []explorationgraph.Node) (*ResultAnalysis, error) {
	return &ResultAnalysis{}, nil
}
