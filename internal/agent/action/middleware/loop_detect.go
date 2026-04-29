package middleware

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/V3teran/liusha/internal/agent/action"
	"github.com/V3teran/liusha/internal/agent/runtime"
)

// CtxKey 是 loop_detect/done_validate 用的 context key 类型；避免裸字符串冲突。
type CtxKey string

const (
	// CtxKeyStepIdx：runtime 注入的"当前 ReAct 步数"，参与 hash 让相邻步即使重复也有机会区分。
	CtxKeyStepIdx CtxKey = "agent.step_idx"
	// CtxKeyCallerSkill：runtime 注入的"当前 Skill 名"，参与 hash 隔离不同 Skill 的同名调用。
	CtxKeyCallerSkill CtxKey = "agent.caller_skill"
)

const (
	loopWindow    = 10 // ring buffer 容量
	loopThreshold = 3  // 连续 ≥N 次同 hash 判定死循环
)

// LoopDetect 工厂：返回检测连续重复动作调用的 ActionMiddleware。
//
// 黑客松借鉴共识 E（代码约束 LoopDetector）。每次调用先算 hash 入 ring buffer，
// 若末尾出现连续 loopThreshold 个相同 hash，立即返回 ErrLoopDetectorAbort。
func LoopDetect() action.ActionMiddleware {
	d := &loopDetector{}

	return func(next action.ActionExecutor) action.ActionExecutor {
		return func(ctx context.Context, name string, args json.RawMessage) (action.Result, error) {
			h := d.hash(ctx, name, args)
			if d.recordAndCheck(h) {
				return action.Result{}, fmt.Errorf("loop_detect 拦截 action=%s: %w", name, runtime.ErrLoopDetectorAbort)
			}
			return next(ctx, name, args)
		}
	}
}

// loopDetector 是状态容器：用 mutex 保护一个 ring buffer。
type loopDetector struct {
	mu    sync.Mutex
	buf   [loopWindow][32]byte
	count int // 已写入的总次数
}

// hash = sha256(name + canonicalArgs + callerSkill + stepIdx%loopWindow)
func (d *loopDetector) hash(ctx context.Context, name string, args json.RawMessage) [32]byte {
	caller, _ := ctx.Value(CtxKeyCallerSkill).(string)
	stepIdx, _ := ctx.Value(CtxKeyStepIdx).(int)

	h := sha256.New()
	h.Write([]byte(name))
	h.Write([]byte{0})
	h.Write(canonicalize(args))
	h.Write([]byte{0})
	h.Write([]byte(caller))
	h.Write([]byte{0})
	fmt.Fprintf(h, "%d", stepIdx%loopWindow)
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// canonicalize 尝试解析 JSON 并 canonical 重序列化；失败则用原 bytes。
func canonicalize(args json.RawMessage) []byte {
	if len(args) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(args, &v); err != nil {
		return []byte(args)
	}
	out, err := json.Marshal(v)
	if err != nil {
		return []byte(args)
	}
	return out
}

// recordAndCheck 写入新 hash，返回是否检测到 ≥loopThreshold 次连续相同。
func (d *loopDetector) recordAndCheck(h [32]byte) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	idx := d.count % loopWindow
	d.buf[idx] = h
	d.count++

	have := d.count
	if have > loopWindow {
		have = loopWindow
	}
	if have < loopThreshold {
		return false
	}
	for back := 0; back < loopThreshold; back++ {
		pos := ((d.count - 1 - back) % loopWindow) + loopWindow
		pos %= loopWindow
		if d.buf[pos] != h {
			return false
		}
	}
	return true
}
