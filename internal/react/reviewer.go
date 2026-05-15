package react

import "context"

// Verdict 决策枚举：
//   - continue  方向正常，继续当前路径
//   - redirect  方向有偏差，把 Hint 注入下一轮 user prompt 引导调整
//   - terminate 当前任务已无进展空间，建议主循环立即终止
const (
	VerdictContinue  = "continue"
	VerdictRedirect  = "redirect"
	VerdictTerminate = "terminate"
)

// Verdict 是 Reviewer 对最近窗口的判决。
//
// Decision 取上述常量之一；Hint 仅在 VerdictRedirect 时有效，会以 user 消息注入下一轮 prompt。
type Verdict struct {
	Decision string
	Hint     string
}

// StepRecord 是滑动窗喂给 Reviewer 的最近 N 步记录。
//
// ObsSummary 字段保留：tool Action 可在 Result.Summary 主动设 ≤200 字摘要——多数 step 用它即可。
// FullObs 是工具完整输出（不截断）—— 仅最近 1 步（window 末尾）填，让 reviewer 能看到
// 关键字（SUCCESS/vulnerable/uid=）防止摘要截断误判进度。零值时 buildReviewerPrompt 回退 ObsSummary。
type StepRecord struct {
	StepIdx    int
	ActionName string
	Args       []byte
	ObsSummary string
	FullObs    string
}

// Reviewer 在 ReAct 循环每 N 步触发一次，用最近窗口判断"该不该继续 / 怎么改向"。
//
// 由 T23.5 用 LLM 实现；T23 默认注入 NoopReviewer 让循环行为与旧版兼容。
type Reviewer interface {
	Evaluate(ctx context.Context, window []StepRecord) Verdict
}

// NoopReviewer 永远返回 continue，便于在不需 Reviewer 的场景注入。
type NoopReviewer struct{}

// Evaluate 实现 Reviewer。
func (NoopReviewer) Evaluate(context.Context, []StepRecord) Verdict {
	return Verdict{Decision: VerdictContinue}
}
