package executor

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/explorationgraph"
)

type flipReader struct{ aborted atomic.Bool }

func (f *flipReader) GetNode(_ context.Context, id string) (*explorationgraph.Node, error) {
	st := explorationgraph.StateRunning
	if f.aborted.Load() {
		st = explorationgraph.StateAborted
	}
	return &explorationgraph.Node{ID: id, State: &st}, nil
}

// kill 发生在长工具执行期间：action 级 context 必须被取消，在途工具随之退出。
func TestWatchKill_CancelsInFlightWork(t *testing.T) {
	r := &flipReader{}
	ctx, ks, stop := watchKill(context.Background(), r, "a1", 5*time.Millisecond, zerolog.Nop())
	defer stop()

	go func() {
		time.Sleep(20 * time.Millisecond)
		r.aborted.Store(true)
	}()
	select {
	case <-ctx.Done(): // 模拟长工具：仅在 context 取消时返回
	case <-time.After(2 * time.Second):
		t.Fatal("kill 后 action 级 context 未被取消")
	}
	if !ks.Killed() {
		t.Fatal("kill 后 Killed() 应为 true")
	}
}

// 正常结束：stop 回收轮询，不误报 kill。
func TestWatchKill_StopWithoutKill(t *testing.T) {
	r := &flipReader{}
	_, ks, stop := watchKill(context.Background(), r, "a1", 5*time.Millisecond, zerolog.Nop())
	time.Sleep(15 * time.Millisecond)
	stop()
	if ks.Killed() {
		t.Fatal("未 kill 不应置位")
	}
}
