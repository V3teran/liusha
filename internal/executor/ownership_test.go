package executor

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/explorationgraph"
)

// switchReader 模拟动作在执行期间被外部改写：change 置位后返回 after 描述的节点。
type switchReader struct {
	change atomic.Bool
	after  explorationgraph.Node
}

func (f *switchReader) GetNode(_ context.Context, id string) (*explorationgraph.Node, error) {
	if f.change.Load() {
		n := f.after
		n.ID = id
		return &n, nil
	}
	st := explorationgraph.StateRunning
	return &explorationgraph.Node{ID: id, State: &st, Metadata: []byte(`{"claimed_by":"run-1"}`)}, nil
}

// 动作在执行期间被收回（否决、任务收口、被新 run 回收后重新认领）：action 级 context
// 必须被取消，在途工具随之退出。
func TestWatchOwnership_CancelsWhenRevoked(t *testing.T) {
	rejected := explorationgraph.StateRejected
	running := explorationgraph.StateRunning
	cases := map[string]explorationgraph.Node{
		"否决":           {State: &rejected, Metadata: []byte(`{"claimed_by":"run-1"}`)},
		"被别的 run 重新认领": {State: &running, Metadata: []byte(`{"claimed_by":"run-2"}`)},
	}
	for name, after := range cases {
		t.Run(name, func(t *testing.T) {
			r := &switchReader{after: after}
			ctx, own, stop := watchOwnership(context.Background(), r, "a1", "run-1", 5*time.Millisecond, zerolog.Nop())
			defer stop()

			go func() {
				time.Sleep(20 * time.Millisecond)
				r.change.Store(true)
			}()
			select {
			case <-ctx.Done(): // 模拟长工具：仅在 context 取消时返回
			case <-time.After(2 * time.Second):
				t.Fatal("动作被收回后 action 级 context 未被取消")
			}
			if !own.Lost() {
				t.Fatal("动作被收回后 Lost() 应为 true")
			}
		})
	}
}

// 正常结束：stop 回收轮询，不误报收回。
func TestWatchOwnership_StopWhileHeld(t *testing.T) {
	r := &switchReader{}
	_, own, stop := watchOwnership(context.Background(), r, "a1", "run-1", 5*time.Millisecond, zerolog.Nop())
	time.Sleep(15 * time.Millisecond)
	stop()
	if own.Lost() {
		t.Fatal("仍持有时不应置位")
	}
}
