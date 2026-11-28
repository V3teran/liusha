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

// seedAction 建一个待执行的动作，创建时刻严格递增（同档里按它排先后）。
func seedAction(t *testing.T, store *explorationgraph.Store, id string) {
	t.Helper()
	seedActionAs(t, store, id, false, false)
}

// seedActionAs 建一个待执行的动作：user 是用户指派的，urgent 是规划者标了加急的。
func seedActionAs(t *testing.T, store *explorationgraph.Store, id string, user, urgent bool) explorationgraph.Node {
	t.Helper()
	open := explorationgraph.StateOpen
	source := explorationgraph.SourcePlanner
	if user {
		source = explorationgraph.SourceUser
	}
	action := explorationgraph.Node{
		ID: id, TaskID: "t1", Kind: core.KindAction, State: &open, SourceType: source, Urgent: urgent,
		Content: []byte(`{"instruction":"` + id + `"}`), CreatedAt: time.Now(),
	}
	_, err := store.CreateNode(context.Background(), action)
	require.NoError(t, err)
	time.Sleep(time.Millisecond) // 创建时刻严格递增
	return action
}

// seedLead 给动作 claimant 认领一条线索（pursues 边）。
func seedLead(t *testing.T, store *explorationgraph.Store, claimant string) {
	t.Helper()
	require.NoError(t, store.CreateNodeWithEdges(context.Background(), explorationgraph.Node{
		ID: "lead-of-" + claimant, TaskID: "t1", Kind: core.KindObservation, SourceType: explorationgraph.SourceExecutor,
		Lead:    &explorationgraph.LeadSpec{Next: "n", Question: "q", Potential: "p"},
		Content: []byte(`{"category":"access","statement":"探通一条路"}`),
	}, []explorationgraph.Edge{{TaskID: "t1", SrcID: claimant, Rel: explorationgraph.RelPursues, DstID: "lead-of-" + claimant}}))
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

// 派发先后由规则定：用户指派 → 加急 → 认领线索的 → 其余，同档按创建先后。不靠模型打分。
func TestRun_DispatchesByFixedOrderThenAge(t *testing.T) {
	store := explorationgraph.NewMemoryStore()
	// 故意把先该跑的排在后面创建：次序与创建先后相反时才看得出档位在起作用
	seedActionAs(t, store, "rest-old", false, false)
	seedActionAs(t, store, "rest-new", false, false)
	claimant := seedActionAs(t, store, "claimant", false, false)
	seedLead(t, store, claimant.ID)
	seedActionAs(t, store, "urgent", false, true)
	seedActionAs(t, store, "user", true, false)

	exec := newGatedExecutor()
	a := NewAgent(AgentConfig{
		TaskID: "t1", AgentRunID: "run-1", Graph: store, Executor: exec,
		EventBus: bus.New(context.Background()), Logger: zerolog.Nop(), MaxParallel: 1,
	})
	stop := runAgent(t, a)
	defer stop()

	exec.waitStarted(t, "rest-new") // 等最后一个也开跑：没有闸门的动作即开即完，逐个接上
	exec.mu.Lock()
	defer exec.mu.Unlock()
	assert.Equal(t, []string{"user", "urgent", "claimant", "rest-old", "rest-new"}, exec.started)
}

// 用户指派的动作先于加急：人在运行中的纠偏排在最前。
func TestRun_UserAssignedRunsBeforeUrgent(t *testing.T) {
	store := explorationgraph.NewMemoryStore()
	seedActionAs(t, store, "urgent", false, true)
	seedActionAs(t, store, "user", true, false)
	seedActionAs(t, store, "user-2", true, false)

	exec := newGatedExecutor()
	a := NewAgent(AgentConfig{
		TaskID: "t1", AgentRunID: "run-1", Graph: store, Executor: exec,
		EventBus: bus.New(context.Background()), Logger: zerolog.Nop(), MaxParallel: 1,
	})
	stop := runAgent(t, a)
	defer stop()

	exec.waitStarted(t, "urgent")
	exec.mu.Lock()
	defer exec.mu.Unlock()
	assert.Equal(t, []string{"user", "user-2", "urgent"}, exec.started, "同为用户指派的按创建先后")
}

// 认领先索的动作先于铺开的：深挖的边际收益高于再摸一个对象面。
func TestLeadClaimantsAndOrder(t *testing.T) {
	store := explorationgraph.NewMemoryStore()
	plain := seedActionAs(t, store, "plain", false, false)
	follower := seedActionAs(t, store, "follower", false, false)
	seedLead(t, store, follower.ID)
	claimants, err := store.LeadClaimants(context.Background(), "t1")
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{"follower": true}, claimants, "只有带 pursues 边的动作认领了线索")
	assert.True(t, dispatchOrder(follower, plain, claimants), "后创建的、认领先索的排在先")
	assert.False(t, dispatchOrder(plain, follower, claimants))
	same := plain
	same.ID = "aaa"
	assert.True(t, dispatchOrder(same, plain, nil), "同档同时刻按 ID 定序")
}

// 没有整批屏障：一个慢动作在跑时，空出的位置立即认领新提出的动作，而不是等整批跑完。
func TestRun_RefillsSlotsWithoutWaitingForSlowAction(t *testing.T) {
	store := explorationgraph.NewMemoryStore()
	seedAction(t, store, "slow")
	seedAction(t, store, "fast")

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
	seedAction(t, store, "later")
	eventBus.PublishActionProposed("t1", "later")

	exec.waitStarted(t, "later") // slow 仍在执行
	n, err := store.GetNode(context.Background(), "slow")
	require.NoError(t, err)
	assert.Equal(t, explorationgraph.StateRunning, *n.State, "慢动作仍在执行，新动作已被认领")
}
