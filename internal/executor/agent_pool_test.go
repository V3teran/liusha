package executor

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/bus"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
)

// gatedExecutor 记录动作的开始顺序；gates 里登记的动作阻塞到对应通道关闭才结束。
type gatedExecutor struct {
	mu      sync.Mutex
	started []string
	notify  chan string
	gates   map[string]chan struct{}
}

func newGatedExecutor(gated ...string) *gatedExecutor {
	g := &gatedExecutor{notify: make(chan string, 16), gates: map[string]chan struct{}{}}
	for _, id := range gated {
		g.gates[id] = make(chan struct{})
	}
	return g
}

func (g *gatedExecutor) Execute(ctx context.Context, action explorationgraph.Node) (Outcome, error) {
	g.mu.Lock()
	g.started = append(g.started, action.ID)
	g.mu.Unlock()
	g.notify <- action.ID
	if gate, ok := g.gates[action.ID]; ok {
		select {
		case <-gate:
		case <-ctx.Done():
			return OutcomeDone, ctx.Err()
		}
	}
	return OutcomeDone, nil
}

// waitStarted 等到动作开始执行：以已记录的开始列表为准，通知只用来唤醒检查——
// 并发开始的动作先后不定，不能按通知顺序消费。
func (g *gatedExecutor) waitStarted(t *testing.T, id string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		g.mu.Lock()
		started := slices.Contains(g.started, id)
		g.mu.Unlock()
		if started {
			return
		}
		select {
		case <-g.notify:
		case <-deadline:
			t.Fatalf("动作 %s 未开始执行", id)
		}
	}
}

func seedAction(t *testing.T, store *explorationgraph.Store, id string, priority explorationgraph.Priority) {
	t.Helper()
	open := explorationgraph.StateOpen
	_, err := store.CreateNode(context.Background(), explorationgraph.Node{
		ID: id, TaskID: "t1", Kind: core.KindAction, State: &open, Priority: priority,
		SourceType: explorationgraph.SourcePlanner, Content: []byte(`{"instruction":"` + id + `"}`),
		CreatedAt: time.Now(),
	})
	require.NoError(t, err)
	time.Sleep(time.Millisecond) // 创建时刻严格递增
}

func runAgent(t *testing.T, a *Agent) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = a.Run(ctx)
		close(done)
	}()
	return func() {
		cancel()
		<-done
	}
}

// 空位按「优先级高 → 创建早」认领：用户指派的高优先级动作不必排在先创建的低优先级动作之后。
func TestRun_ClaimsByPriorityThenAge(t *testing.T) {
	store := explorationgraph.NewMemoryStore()
	seedAction(t, store, "low", explorationgraph.PriorityLow)
	seedAction(t, store, "high-old", explorationgraph.PriorityHigh)
	seedAction(t, store, "critical", explorationgraph.PriorityCritical)
	seedAction(t, store, "high-new", explorationgraph.PriorityHigh)

	exec := newGatedExecutor()
	a := NewAgent(AgentConfig{
		TaskID: "t1", AgentRunID: "run-1", Graph: store, Executor: exec,
		EventBus: bus.New(context.Background()), Logger: zerolog.Nop(), MaxParallel: 1,
	})
	stop := runAgent(t, a)
	defer stop()

	exec.waitStarted(t, "low")
	exec.mu.Lock()
	defer exec.mu.Unlock()
	assert.Equal(t, []string{"critical", "high-old", "high-new", "low"}, exec.started)
}

// 没有整批屏障：一个慢动作在跑时，空出的位置立即认领新提出的动作，而不是等整批跑完。
func TestRun_RefillsSlotsWithoutWaitingForSlowAction(t *testing.T) {
	store := explorationgraph.NewMemoryStore()
	seedAction(t, store, "slow", explorationgraph.PriorityHigh)
	seedAction(t, store, "fast", explorationgraph.PriorityMedium)

	exec := newGatedExecutor("slow")
	eventBus := bus.New(context.Background())
	a := NewAgent(AgentConfig{
		TaskID: "t1", AgentRunID: "run-1", Graph: store, Executor: exec,
		EventBus: eventBus, Logger: zerolog.Nop(), MaxParallel: 2,
	})
	stop := runAgent(t, a)
	defer stop()
	defer close(exec.gates["slow"])

	exec.waitStarted(t, "slow")
	exec.waitStarted(t, "fast")
	seedAction(t, store, "later", explorationgraph.PriorityMedium)
	eventBus.PublishActionProposed("t1", "later")

	exec.waitStarted(t, "later") // slow 仍在执行
	n, err := store.GetNode(context.Background(), "slow")
	require.NoError(t, err)
	assert.Equal(t, explorationgraph.StateRunning, *n.State, "慢动作仍在执行，新动作已被认领")
}
