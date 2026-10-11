package main

import (
	"context"
	"sync"
	"time"

	"github.com/V3teran/liusha/internal/tools"
)

// activityTracker 记录各 action 最近一次工具活动的时刻（进程内）。
// monitor 的 kill 决策依据：图上"无输出"是长 ReAct 执行期的正常形态
// （观察在 action 完成时才收割），工具活动才是活性的真实信号。
// runner 单实例部署，进程内 map 即可；runner 重启随进程清零（无害——
// 监控只会在缺数据时保守不杀）。
type activityTracker struct {
	mu   sync.Mutex
	last map[string]time.Time
}

func newActivityTracker() *activityTracker {
	return &activityTracker{last: map[string]time.Time{}}
}

// touch 记录一次活动（工具拦截器在每个工具调用后调用）。
func (t *activityTracker) touch(ctx context.Context) {
	if actionID := tools.ActionIDFromContext(ctx); actionID != "" {
		t.mu.Lock()
		t.last[actionID] = time.Now()
		t.mu.Unlock()
	}
}

// idleFor 返回该 action 距最近一次工具活动的时长；无记录返回 (0, false)。
func (t *activityTracker) idleFor(actionID string) (time.Duration, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	ts, ok := t.last[actionID]
	if !ok {
		return 0, false
	}
	return time.Since(ts), true
}
