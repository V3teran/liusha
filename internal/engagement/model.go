// Package engagement 实现 engagement 聚合根的 model 与 store。
//
// engagement 是一次「渗透会话」（红队术语，业界一致：Burp Project / Cobalt Strike Engagement）。
// 一次会话可挂 1+ host：proxy 模式下代理开启期间任意 host 流量都属同一 engagement，
// 按 24h 时间窗轮转：
//   - scope = {} 或 {"any": true}（接受任意 host）
//   - expires_at = created_at + 24h
//   - 同时只允许 1 个 active proxy session（DB 唯一索引 engagement_active_proxy_uniq）
//
// browser 模式（单 host + 无 TTL）的 schema 与 Store API 已在 v0034 移除——未来
// 需要时回退 0034 + 加 CreateBrowserScan 即可，不再保留过早设计。
//
// 短期工作笔记 notes 已迁出 PG，由 internal/notes 包（Redis）实现，按
// (engagement_id, host) 切分隔离 hunter 工作面。
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
//
// v0033 升级：
//   - 删 TargetHost 单字段（per-host engagement 是错误抽象）
//   - 加 Scope（jsonb）描述会话作用域：proxy 模式固定 {"any":true}
//   - 加 ExpiresAt（proxy 模式 = created_at + 24h）
//
// v0034：browser 模式从 CHECK 约束移除，schema 仅接受 mode='proxy'。
type Engagement struct {
	ID            string
	Mode          Mode
	Scope         json.RawMessage // proxy 固定 {"any":true}，未来 browser 复活时可填 {"hosts":[...]}
	Status        Status
	CreatedAt     time.Time
	ExpiresAt     *time.Time // proxy 模式 = created_at + 24h（v0034 起永远非 nil）
	EndedAt       *time.Time
	ErrorMessage  string
	FlowCount     int
	FindingCount  int
	AgentRunCount int
}
