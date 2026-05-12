// Package finding 实现 finding 表持久化层：
// 自由文本 summary 主体 + 自由文本 severity（前端按前缀配色）+ 可选 evidence jsonb。
// LLM 自决全部表达（无 kind / confidence / dedup_key 等强结构化约束）。
//
// dedup 由 LLM 调用方自决：写 finding 前先 findings() 查 host 已有的，自己判要不要再写。
// Save 是 append-only：每次都 INSERT 新行；OnSaved hook 触发 lesson 蒸馏。
package finding

import (
	"encoding/json"
	"time"
)

// VulnFinding 是 finding 表行的 Go 表示。
//
// Host 必填；Save 内空字符串校验防漏填。
// SourceFlowID 可空（不绑定具体流量时 nil）。
// Target / Evidence 为 nil 时 Save 自动落空对象 '{}'。
type VulnFinding struct {
	ID           string
	EngagementID string
	TaskID       *string
	SourceFlowID *int64
	Host         string
	// Severity 自由文本（建议 critical/high/medium/low/info 保持配色一致；
	// 其他值前端配色退化为蓝色）；DB 列无 enum CHECK 约束。
	Severity string
	// Summary 是漏洞描述的核心载体：自由文本写发现是什么 / 怎么验证 / 推理依据。
	// 工具层（write_finding）强制非空。
	Summary   string
	Target    json.RawMessage
	Evidence  json.RawMessage
	CreatedAt time.Time
}
