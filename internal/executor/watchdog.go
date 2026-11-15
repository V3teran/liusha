package executor

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ActivityTracker 记录每个在途动作最近一次有进展的时刻：一次工具调用结束、一步 LLM
// 推理结束都算进展。图上「没有输出」是长 ReAct 执行期的常态（观察在动作结束时才落图），
// 进展才是动作是否活着的信号。进程内记录：动作的执行者与看门狗在同一进程。
type ActivityTracker struct {
	mu   sync.Mutex
	last map[string]time.Time
}

// NewActivityTracker 创建活动记录器。
func NewActivityTracker() *ActivityTracker {
	return &ActivityTracker{last: map[string]time.Time{}}
}

// Touch 记一次进展（actionID 为空时忽略：不属于任何动作的调用）。
func (t *ActivityTracker) Touch(actionID string) {
	if t == nil || actionID == "" {
		return
	}
	t.mu.Lock()
	t.last[actionID] = time.Now()
	t.mu.Unlock()
}

// lastActivity 返回动作最近一次进展的时刻（无记录为零值）。
func (t *ActivityTracker) lastActivity(actionID string) time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.last[actionID]
}

// forget 清除动作的记录（动作结束后调用）。
func (t *ActivityTracker) forget(actionID string) {
	t.mu.Lock()
	delete(t.last, actionID)
	t.mu.Unlock()
}

// stallError 是看门狗取消停摆动作时携带的原因。
type stallError struct{ idle time.Duration }

func (e *stallError) Error() string {
	return fmt.Sprintf("执行停摆：%s 内无任何工具调用或 LLM 推理进展", e.idle.Round(time.Second))
}

// StallTimeout 是判定动作停摆的无进展时长：须长于单步最长的合法耗时——一次 LLM
// 调用（单步 LLM 看门狗上限）加一次工具调用（工具超时上限）——再留一分钟余量。
func StallTimeout(stepLLM, stepTool time.Duration) time.Duration {
	return stepLLM + stepTool + time.Minute
}

// inFlightAction 是一个在途动作的看门狗视图。
type inFlightAction struct {
	started time.Time
	cancel  context.CancelCauseFunc
}

// track 登记在途动作，返回受看门狗管辖的执行 ctx 与登出函数。
func (a *Agent) track(ctx context.Context, actionID string) (context.Context, func()) {
	actionCtx, cancel := context.WithCancelCause(ctx)
	a.inFlightMu.Lock()
	a.inFlight[actionID] = inFlightAction{started: time.Now(), cancel: cancel}
	a.inFlightMu.Unlock()
	return actionCtx, func() {
		a.inFlightMu.Lock()
		delete(a.inFlight, actionID)
		a.inFlightMu.Unlock()
		a.activity.forget(actionID)
		cancel(nil)
	}
}

// watchStalls 是看门狗循环：在途动作超过 stallTimeout 没有任何进展即以停摆为因取消。
// 被取消的动作由执行方按失败结束（见 executeAction），释放并发槽。
func (a *Agent) watchStalls(ctx context.Context) {
	if a.stallTimeout <= 0 {
		return
	}
	ticker := time.NewTicker(a.stallCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			a.cancelStalled(now)
		}
	}
}

func (a *Agent) cancelStalled(now time.Time) {
	a.inFlightMu.Lock()
	defer a.inFlightMu.Unlock()
	for id, f := range a.inFlight {
		last := f.started
		if t := a.activity.lastActivity(id); t.After(last) {
			last = t
		}
		if idle := now.Sub(last); idle > a.stallTimeout {
			a.logger.Warn().Str("action_id", id).Dur("idle", idle).Msg("动作停摆，看门狗取消执行")
			f.cancel(&stallError{idle: idle})
		}
	}
}

// stallCause 返回动作被看门狗判停摆的原因（不是停摆返回 nil）。
func stallCause(actionCtx context.Context) *stallError {
	var stall *stallError
	if errors.As(context.Cause(actionCtx), &stall) {
		return stall
	}
	return nil
}
