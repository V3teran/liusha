package runtime

import "context"

// Verdict 决策枚举：keep_going 继续；steer_with_hint 注入 hint 后继续；abort_low_value 直接终止。
const (
	VerdictKeepGoing = "keep_going"
	VerdictSteer     = "steer_with_hint"
	VerdictAbort     = "abort_low_value"
)

// Verdict 是 Observer 对最近窗口的判决。
//
// Decision 取上述常量之一；Hint 仅在 VerdictSteer 时有效，会以 user 消息注入下一轮 prompt。
type Verdict struct {
	Decision string
	Hint     string
}

// StepRecord 是滑动窗喂给 Observer 的最近 N 步记录。
//
// ObsSummary 期望由 result_compress 中间件填充（≤200 字摘要），
// 避免把整段 LLM 历史塞回 Observer，节省 token。
type StepRecord struct {
	StepIdx    int
	ActionName string
	Args       []byte
	ObsSummary string
}

// Observer 在 ReAct 循环每 N 步触发一次，用最近窗口判断"该不该继续 / 怎么改向"。
//
// 由 T23.5 用 LLM 实现；T23 默认注入 NoopObserver 让循环行为与旧版兼容。
type Observer interface {
	Evaluate(ctx context.Context, window []StepRecord) Verdict
}

// NoopObserver 永远返回 keep_going，便于在不需 Observer 的场景注入。
type NoopObserver struct{}

// Evaluate 实现 Observer。
func (NoopObserver) Evaluate(context.Context, []StepRecord) Verdict {
	return Verdict{Decision: VerdictKeepGoing}
}
