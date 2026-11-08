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

	"github.com/V3teran/liusha/internal/bus"
	"github.com/V3teran/liusha/internal/explorationgraph"
)

// CompletionDetector 是任务终止判定的唯一入口：
// 人工中止 > 暂停冻结 > 步数上限。事件计数（steps/attempts/promoted）必须准确。

func newDetector(t *testing.T, maxSteps int) (*CompletionDetector, bus.EventBus) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	eb := bus.New(ctx)
	d := NewCompletionDetector(Config{
		TaskID:        "t1",
		Graph:         explorationgraph.NewMemoryStore(),
		Bus:           eb,
		Logger:        zerolog.Nop(),
		MaxSteps:      maxSteps,
		CheckInterval: 10 * time.Millisecond,
	})
	return d, eb
}

func TestCompletionDetector_StatsCounting(t *testing.T) {
	d, _ := newDetector(t, 0)

	d.feedEvent(bus.Event{Type: bus.EventActionCompleted, TaskID: "t1", ActionID: "a1"})
	d.feedEvent(bus.Event{Type: bus.EventActionCompleted, TaskID: "t1", ActionID: "a2"})
	d.feedEvent(bus.Event{Type: bus.EventVerificationPassed, TaskID: "t1"})

	got := d.stats()
	assert.Equal(t, 2, got.Steps, "两个 ActionCompleted 应计 2 步")
	assert.Equal(t, 1, got.Promoted, "一个 VerificationPassed 应计 1 晋升")
}

func TestCompletionDetector_AbortWins(t *testing.T) {
	d, _ := newDetector(t, 0)

	d.Abort("control-plane: terminate")
	res, done := d.checkNow()
	require.True(t, done, "人工中止应立即判定完成")
	assert.Equal(t, "control-plane: terminate", res.StopReason)
}

func TestCompletionDetector_MaxSteps(t *testing.T) {
	d, _ := newDetector(t, 3)

	d.feedEvent(bus.Event{Type: bus.EventActionCompleted, TaskID: "t1", ActionID: "a1"})
	d.feedEvent(bus.Event{Type: bus.EventActionCompleted, TaskID: "t1", ActionID: "a2"})
	_, done := d.checkNow()
	assert.False(t, done, "2/3 步不应完成")

	d.feedEvent(bus.Event{Type: bus.EventActionCompleted, TaskID: "t1", ActionID: "a3"})
	res, done := d.checkNow()
	require.True(t, done, "3/3 步应触发完成")
	assert.Equal(t, "max_steps_reached", res.StopReason)
}

func TestCompletionDetector_AbortCarriesPauseReason(t *testing.T) {
	d, _ := newDetector(t, 1)

	d.feedEvent(bus.Event{Type: bus.EventActionCompleted, TaskID: "t1", ActionID: "a1"})
	_, done := d.checkNow()
	assert.True(t, done, "步数满足应触发完成")

	d2, _ := newDetector(t, 1)
	d2.feedEvent(bus.Event{Type: bus.EventActionCompleted, TaskID: "t1", ActionID: "a1"})
	d2.Abort(StopReasonPaused)
	res, done := d2.checkNow()
	require.True(t, done, "人工中止应立即生效（暂停退出复用此通道）")
	assert.Equal(t, StopReasonPaused, res.StopReason, "停止原因必须原样透传给 runner 分流")
}

// checkNow 同步执行一次完成判定（测试的确定性驱动入口；生产走 Start 循环）。
func (d *CompletionDetector) checkNow() (Result, bool) {
	return d.checkCompletion()
}

// feedEvent 直接喂事件给检测器（测试绕过 bus 订阅注入；与 Start 共用 handleEvent）。
func (d *CompletionDetector) feedEvent(event bus.Event) {
	d.handleEvent(event)
}

// stats 获取当前统计快照（非阻塞，测试断言用）。
func (d *CompletionDetector) stats() Result {
	return d.makeResult("in_progress")
}

// agent 致命失败（如 LLM 配置错误）必须终结检测循环并携带错误——否则持续探索
// 模式下任务永不收敛，停在 active。
func TestCompletionDetector_FailStopsWithError(t *testing.T) {
	d := NewCompletionDetector(Config{TaskID: "t1", Bus: bus.New(context.Background()), Logger: zerolog.Nop(),
		CheckInterval: 10 * time.Millisecond})
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
