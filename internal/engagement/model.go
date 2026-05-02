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

// Category 是 memory_facts entry 的子类。AppendFact 按此映射到 jsonb 子键
// （CategoryEvidence → "evidence" 数组；CategoryBoundary → "boundaries" 数组）。
//
// 用 typed enum 替代 raw string 让新增类别（如 future "attack_surface"）必须
// 经过类型 + memoryKey() 显式登记，避免 silent typo。
type Category string

const (
	ModeProxy   Mode = "proxy"
	ModeBrowser Mode = "browser"

	StatusActive   Status = "active"
	StatusAborted  Status = "aborted"
	StatusArchived Status = "archived"

	CategoryEvidence Category = "evidence"
	CategoryBoundary Category = "boundary"
)

// memoryKey 返回 Category 在 memory_facts jsonb 的子键名。
//
// 设计：jsonb 子键名复数（"evidence" 单数巧合，"boundaries" 复数表数组），
// 与 BAC SKILL.md 调用方约定的 entry.category 单数命名解耦。
func (c Category) memoryKey() (string, bool) {
	switch c {
	case CategoryEvidence:
		return "evidence", true
	case CategoryBoundary:
		return "boundaries", true
	default:
		return "", false
	}
}

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
