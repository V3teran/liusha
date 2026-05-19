// Package engagement 实现 engagement 聚合根的 model 与 store。
//
// engagement 是一次「渗透会话」（红队术语：Burp Project / Cobalt Strike Engagement）。
// 两种模式：
//   - passive: 流量驱动（cmd/proxy MITM 拦截），1 会话可挂 1+ host，按 24h 时间窗轮转。
//     scope = {"any":true}；expires_at = created_at + 24h；
//     同时只允许 1 个 active passive session（唯一索引 engagement_active_passive_uniq）。
//   - active: 用户自然语言 brief 主动下发的站点扫描。
//     scope = {"brief":"..."}；expires_at = NULL（任务跑完即终态）；
//     不受唯一约束，可并发多个。
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
	ModePassive Mode = "passive"
	ModeActive  Mode = "active"

	StatusActive   Status = "active"
	StatusAborted  Status = "aborted"
	StatusArchived Status = "archived"
)

// Engagement 是 engagement 表行的 Go 表示。
type Engagement struct {
	ID            string
	Mode          Mode
	Scope         json.RawMessage // passive: {"any":true}；active: {"brief":"..."}
	Status        Status
	CreatedAt     time.Time
	ExpiresAt     *time.Time // passive session 必填；active 模式为 nil
	EndedAt       *time.Time
	ErrorMessage  string
	// FlowCount / FindingCount / AgentRunCount：增量维护的近似计数，**允许漂移**。
	// active 运行期 Increment*Count 是 best-effort，失败仅 log warn 不阻塞业务；
	// 仅 Abort 路径用 SELECT count(*) 精确兜底落库。所以：
	//   - 运行中读到的计数 = 近似（可能比真实值少几条）
	//   - archived/aborted 后读到的 = 精确
	// httpapi 直接返回这三个字段，viewer 实时刷新看到的是近似值（差几条可接受）。
	FlowCount     int
	FindingCount  int
	AgentRunCount int
}
