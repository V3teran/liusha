// Package finding 实现 finding 表持久化层：
// 以 (engagement_id, dedup_key) 为 UNIQUE 去重键，
// Save 路径冲突时合并 evidence（jsonb || EXCLUDED.evidence）+ 推进 updated_at；
// 新发现/合并后通过 OnSaved hook 异步广播给订阅者（如 T23.5 Distill）。
package finding

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

// Confidence 是 finding.confidence 文本枚举。
type Confidence string

const (
	ConfidenceUnverified Confidence = "unverified"
	ConfidenceVerified   Confidence = "verified"
	ConfidenceRejected   Confidence = "rejected"
)

// Finding 是 finding 表行的 Go 表示。
// Target / Evidence / Payload 为 nil 时 Save 自动落空对象 '{}'。
type Finding struct {
	ID           string
	EngagementID string
	TaskID       *string
	Kind         string
	Severity     Severity
	Title        string
	Target       json.RawMessage
	Evidence     json.RawMessage
	Payload      json.RawMessage
	Tool         string
	Confidence   Confidence
	DedupKey     string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}
