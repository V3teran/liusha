// Package engagement 实现 engagement 聚合根的 model 与 store。
// engagement 是一次"扫描会话"，按 (tenant, scope_host) 懒创建；active 唯一。
// 三层 memory（facts / ideas / hints）实现黑客松借鉴的状态板。
package engagement

import (
	"encoding/json"
	"time"
)

// Mode 表示 engagement 的工作模式。
type Mode string

// Status 表示 engagement 的生命周期状态。
type Status string

const (
	ModeProxy   Mode = "proxy"
	ModeBrowser Mode = "browser"

	StatusActive   Status = "active"
	StatusAborted  Status = "aborted"
	StatusArchived Status = "archived"
)

// Engagement 是 engagement 表行的 Go 表示。
// MemoryFacts/Ideas/Hints 是 jsonb 列的原始字节，调用方按需 unmarshal。
type Engagement struct {
	ID             string
	TenantID       string
	Mode           Mode
	ScopeHost      string
	Status         Status
	MemoryFacts    []byte // jsonb: {evidence: [...], boundaries: [...]}
	MemoryIdeas    []byte // jsonb: {hypotheses: [{direction, status, ts}]}
	MemoryHints    []byte // jsonb: {hints: [{from_skill, content, priority, ts}]}
	CreatedAt      time.Time
	LastActivityAt time.Time
}

// State 是 ReadState action 返回的合并视图，对应 spec §3.4 字段。
type State struct {
	Facts json.RawMessage `json:"facts"`
	Ideas json.RawMessage `json:"ideas"`
	Hints json.RawMessage `json:"hints"`
}
