// Package runtime 提供 ReAct 循环的运行时错误（loop_detect / done_validate 中间件依赖）。
//
// 注意：T22.5 仅落地 errors.go；ReAct 主循环（budget/runtime.go）由 T23 实现。
package runtime

import (
	"errors"
	"fmt"
	"strings"
)

// ErrLoopDetectorAbort 由 loop_detect 中间件抛出：连续 N 次同 hash 的动作调用被判定为死循环。
var ErrLoopDetectorAbort = errors.New("loop detector: same action+args repeated, aborting")

// ErrDoneNotReady 由 done_validate 中间件抛出：LLM 想 done 但 Skill 注册的 DoneValidator 拒绝。
//
// Missing 列出还缺哪些步骤/字段，runtime 会把它喂回 Observer 让 LLM 继续。
type ErrDoneNotReady struct {
	Missing []string
}

func (e ErrDoneNotReady) Error() string {
	return fmt.Sprintf("done not ready: missing %s", strings.Join(e.Missing, ", "))
}

// IsDoneNotReady 是 errors.As 的便捷封装。
func IsDoneNotReady(err error) (ErrDoneNotReady, bool) {
	var e ErrDoneNotReady
	if errors.As(err, &e) {
		return e, true
	}
	return ErrDoneNotReady{}, false
}
