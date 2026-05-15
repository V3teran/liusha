// Package engagement 实现 engagement 聚合根的 model 与 store。
//
// engagement 是一次「渗透会话」（红队术语：Burp Project / Cobalt Strike Engagement）。
// 一次会话可挂 1+ host：代理开启期间任意 host 流量都属同一 engagement，按 24h
// 时间窗轮转：
//   - scope: jsonb，proxy 模式固定 {"any":true}（接受任意 host）
//   - expires_at: created_at + 24h
//   - 同时只允许 1 个 active proxy session（唯一索引 engagement_active_proxy_uniq）
//
// 短期工作笔记 notes 在 Redis（internal/notes 包），按 (engagement_id, host) 切分。
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
	ModeProxy Mode = "proxy"

	StatusActive   Status = "active"
	StatusAborted  Status = "aborted"
	StatusArchived Status = "archived"
)

// Engagement 是 engagement 表行的 Go 表示。
type Engagement struct {
	ID            string
	Mode          Mode
	Scope         json.RawMessage // proxy 固定 {"any":true}
	Status        Status
	CreatedAt     time.Time
	ExpiresAt     *time.Time // proxy session 必填：created_at + 24h
	EndedAt       *time.Time
	ErrorMessage  string
	FlowCount     int
	FindingCount  int
	AgentRunCount int
}
