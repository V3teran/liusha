package cognition

import (
	"context"
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

func newDetector(t *testing.T, maxSteps int) (*CompletionDetector, bus.Bus) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	eb := bus.New(ctx)
	d := NewCompletionDetector(Config{
		TaskID:        "t1",
		World:         explorationgraph.NewMemoryStore(),
		Bus:           eb,
		Logger:        zerolog.Nop(),
		MaxSteps:      maxSteps,
		CheckInterval: 10 * time.Millisecond,
	})
	return d, eb
}

func TestCompletionDetector_StatsCounting(t *testing.T) {
	d, _ := newDetector(t, 0)

	d.FeedEvent(bus.Event{Type: bus.EventActionCompleted, TaskID: "t1", ActionID: "a1"})
	d.FeedEvent(bus.Event{Type: bus.EventActionCompleted, TaskID: "t1", ActionID: "a2"})
	d.FeedEvent(bus.Event{Type: bus.EventVerificationPassed, TaskID: "t1"})

	got := d.GetStats()
	assert.Equal(t, 2, got.Steps, "两个 ActionCompleted 应计 2 步")
	assert.Equal(t, 1, got.Promoted, "一个 VerificationPassed 应计 1 晋升")
}

func TestCompletionDetector_AbortWins(t *testing.T) {
	d, _ := newDetector(t, 0)

	d.Abort("control-plane: terminate")
	res, done := d.CheckNow()
	require.True(t, done, "人工中止应立即判定完成")
	assert.Equal(t, "control-plane: terminate", res.StopWhy)
}

func TestCompletionDetector_MaxSteps(t *testing.T) {
	d, _ := newDetector(t, 3)

	d.FeedEvent(bus.Event{Type: bus.EventActionCompleted, TaskID: "t1", ActionID: "a1"})
	d.FeedEvent(bus.Event{Type: bus.EventActionCompleted, TaskID: "t1", ActionID: "a2"})
	_, done := d.CheckNow()
	assert.False(t, done, "2/3 步不应完成")

	d.FeedEvent(bus.Event{Type: bus.EventActionCompleted, TaskID: "t1", ActionID: "a3"})
	res, done := d.CheckNow()
	require.True(t, done, "3/3 步应触发完成")
	assert.Equal(t, "max_steps_reached", res.StopWhy)
}

func TestCompletionDetector_PauseFreezesAutoCompletion(t *testing.T) {
	d, _ := newDetector(t, 1)

	d.FeedEvent(bus.Event{Type: bus.EventActionCompleted, TaskID: "t1", ActionID: "a1"})
	_, done := d.CheckNow()
	assert.True(t, done, "未暂停时应触发步数完成")

	d2, _ := newDetector(t, 1)
	d2.FeedEvent(bus.Event{Type: bus.EventActionCompleted, TaskID: "t1", ActionID: "a1"})
	d2.Pause()
	_, done = d2.CheckNow()
	assert.False(t, done, "暂停期间自动完成判定必须冻结")

	// terminate 仍然可以越过暂停
	d2.Abort("terminate")
	_, done = d2.CheckNow()
	assert.True(t, done, "人工中止应越过暂停生效")

	d2.Resume()
}
