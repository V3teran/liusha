package monitor

import (
	"context"
	"sync"

	"github.com/V3teran/liusha/internal/explorationgraph"
)

// seenSurface 记下一轮评估最近一次经 task_survey 看到的图面。决策要基于本轮亲见的
// 态势：没看过就不能发决策；重规划请求带上这份图面，规划者处理时据它判断请求是否已过时。
type seenSurface struct {
	mu      sync.Mutex
	surface *explorationgraph.Surface
}

type seenSurfaceKey struct{}

// withSeenSurface 为一轮评估开一份所见记录（每轮评估各自一份，互不沿用）。
func withSeenSurface(ctx context.Context) context.Context {
	return context.WithValue(ctx, seenSurfaceKey{}, &seenSurface{})
}

// recordSeen 记下本轮看到的图面；ctx 不在一轮评估里时什么都不做。
func recordSeen(ctx context.Context, s explorationgraph.Surface) {
	if r, ok := ctx.Value(seenSurfaceKey{}).(*seenSurface); ok {
		r.mu.Lock()
		r.surface = &s
		r.mu.Unlock()
	}
}

// lastSeen 返回本轮最近一次看到的图面（没看过返回 nil）。
func lastSeen(ctx context.Context) *explorationgraph.Surface {
	r, ok := ctx.Value(seenSurfaceKey{}).(*seenSurface)
	if !ok {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.surface == nil {
		return nil
	}
	s := *r.surface
	return &s
}
