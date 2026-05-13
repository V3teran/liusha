// Package runtime 提供 ReAct 循环的运行时错误（done_validate 中间件依赖）。
//
// LoopDetector 已砍——MaxSteps + DoneValidator 是足够的死循环兜底。
package react

import (
	"errors"
	"fmt"
	"strings"
)

// ErrDoneNotReady 由 done_validate 中间件抛出：LLM 想 done 但 Skill 注册的 DoneValidator 拒绝。
//
// Missing 列出还缺哪些步骤/字段，runtime 会把它喂回 Reviewer 让 LLM 继续。
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
