package cognition

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/registry"
)

// recordingActivity 记下登记到存活心跳的在途操作（RunActivity 的替身）。
type recordingActivity struct {
	mu        sync.Mutex
	deadlines map[string][]time.Time // run → 已撤销的登记各自的截止时间
	live      map[string]int         // run → 仍未撤销的登记数
}

func newRecordingActivity() *recordingActivity {
	return &recordingActivity{deadlines: map[string][]time.Time{}, live: map[string]int{}}
}

func (a *recordingActivity) Busy(runID string, until time.Time) func() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.live[runID]++
	return func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		a.live[runID]--
		a.deadlines[runID] = append(a.deadlines[runID], until)
	}
}

func (a *recordingActivity) inFlight(runID string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.live[runID]
}

// 工具在途期间，它的超时被登记为所属 run 的在途操作，结束即撤销：一个跑十分钟的命令在途时，
// 恢复监控据此知道这轮 run 没有停摆。没有注入 RunActivity（或不属于任何 run）时不登记。
func TestToolRecordInterceptor_RegistersToolTimeoutWithRunHeartbeat(t *testing.T) {
	activity := newRecordingActivity()
	d := &Deps{Logger: zerolog.Nop(), RunActivity: activity}
	tool := echoTool{}
	next := func(ctx context.Context, _ registry.Tool, _ []byte) (registry.ToolResult, error) {
		if got := activity.inFlight("run-1"); got != 1 {
			t.Fatalf("工具执行期间应登记 1 个在途操作，实际 %d", got)
		}
		return registry.ToolResult{Output: "ok"}, nil
	}

	deadline := time.Now().Add(10 * time.Minute)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	if _, err := d.toolRecordInterceptor("run-1", "task-1", nil)(ctx, tool, nil, next); err != nil {
		t.Fatal(err)
	}
	if got := activity.inFlight("run-1"); got != 0 {
		t.Fatalf("工具结束后应撤销登记，实际剩 %d", got)
	}
	if got := activity.deadlines["run-1"]; len(got) != 1 || !got[0].Equal(deadline) {
		t.Fatalf("登记的截止时间应是工具的超时（ctx 的截止时间），实际 %v", got)
	}

	// 没有注入登记处：照常执行，不出错。
	bare := &Deps{Logger: zerolog.Nop()}
	ran := false
	if _, err := bare.toolRecordInterceptor("run-1", "task-1", nil)(ctx, tool, nil, func(context.Context, registry.Tool, []byte) (registry.ToolResult, error) {
		ran = true
		return registry.ToolResult{Output: "ok"}, nil
	}); err != nil || !ran {
		t.Fatalf("未注入 RunActivity 时工具应照常执行，err=%v ran=%v", err, ran)
	}
}
