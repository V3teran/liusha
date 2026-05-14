// Package engagement 实现 engagement 聚合根的 model 与 store。
// engagement 是一次"扫描会话"，按 target_host 懒创建；active 唯一。
//
// 短期工作笔记 notes 已迁出 PG，由 internal/notes 包（Redis）实现，不再属于本 model。
package engagement

import (
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
//
// v0010：删除 LastActivityAt 字段（Touch() 无人调用、仅 Abort 时设一次但无下游消费者）。
// v0015：加 EndedAt / ErrorMessage / *Count 字段做进度统计。
//   - EndedAt 仅 Abort 时填；active 状态保持 nil。
//   - *Count 字段 active 期间由 vulnfinding/flow/reactrun 写路径 best-effort 增量；
//     Abort 时事务内 SELECT count(*) 重算精确兜底。
type Engagement struct {
	ID            string
	Mode          Mode
	TargetHost    string
	Status        Status
	CreatedAt     time.Time
	EndedAt       *time.Time
	ErrorMessage  string
	FlowCount     int
	FindingCount  int
	AgentRunCount int
}
