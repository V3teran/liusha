// Package finding 实现 finding 表持久化层：
// 以 (engagement_id, dedup_key) 为 UNIQUE 去重键，
// Save 路径冲突时合并 evidence（jsonb || EXCLUDED.evidence）+ 推进 updated_at；
// 新发现/合并后通过 OnSaved hook 异步广播给订阅者（如 T23.5 LessonExtract）。
package vulnfinding

import (
	"encoding/json"
	"time"
)

// Severity 是 finding.severity 文本枚举。
type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityLow      Severity = "low"
	SeverityMedium   Severity = "medium"
	SeverityHigh     Severity = "high"
	SeverityCritical Severity = "critical"
)

// Confidence 是 finding.confidence 文本枚举——LLM 自评置信度三态：
//
//   - high   ：原生工具（如 sqlmap）默认参数即坐实
//   - medium ：升级参数 / 自构 PoC 复测才坐实，或仅有强 body_hint 关键字
//   - low    ：仅相似度差分 / 弱关键字，证据链单薄
type Confidence string

const (
	ConfidenceHigh   Confidence = "high"
	ConfidenceMedium Confidence = "medium"
	ConfidenceLow    Confidence = "low"
)

// VulnFinding 是 finding 表行的 Go 表示。
// Target / Evidence 为 nil 时 Save 自动落空对象 '{}'。
//
// Host 是从 target.host 提取的显式列（v1.2 加），用于 (host, dedup_key) 全局唯一索引
// 跨 engagement 去重；caller 必填非空（Save 内部空字符串校验防漏填）。
//
// SourceFlowID 指向触发本次 finding 的 http_flow.id（FK SET NULL）。可空：
// 主 ReAct 直发 finding 不带 flow_id 时 nil；存量 finding 也是 nil。
//
// v0010：删除 Tool/Payload/UpdatedAt 字段（深度复审：tool 永远空、payload 永远 '{}'、
// append-only 后 updated_at 永远 = created_at）。
type VulnFinding struct {
	ID           string
	EngagementID string
	TaskID       *string
	SourceFlowID *int64
	Host         string
	Kind         string
	Severity     Severity
	Title        string
	Target       json.RawMessage
	Evidence     json.RawMessage
	Confidence   Confidence
	DedupKey     string
	CreatedAt    time.Time
}
