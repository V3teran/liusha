package llm

import "strings"

// ExtractJSON 从 LLM 自由文本回复中截取 JSON 对象正文：取首个 '{' 到末个 '}' 之间
// 的内容。markdown 代码围栏（```json … ```）、前后闲聊都在截取中被剥掉。
// 无 JSON 时返回空串，调用方按解析失败处理。
// planner 规划解析、evaluator 裁决解析、result 分析解析共用本实现。
func ExtractJSON(content string) string {
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start == -1 || end <= start {
		return ""
	}
	return content[start : end+1]
}
