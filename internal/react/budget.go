package react

// Budget 控制 ReAct 循环的资源上限。
//
//   - MaxSteps：最大步数，防止 LLM 无限规划。
//   - MaxTokens：累计 in+out token 上限，0 表示不限。
//   - WatchdogSeconds：单步 LLM 调用 watchdog，0 时由 Run 兜底为 60。
type Budget struct {
	MaxSteps        int
	MaxTokens       int
	WatchdogSeconds int
}
