// Package activescan 实现主动扫描会话的 model + store。
//
// 业务定位：用户自然语言 brief 主动下发的一次性站点扫描。
// brief 是核心实体（"扫这个 URL 找 XSS"），target_host 可选（某些 brief 不绑死单 host）。
//
// 与 passive_session 的差异：
//   - active 按 brief 切（"哪次任务"），passive 按 host 切（"哪个站点被监控"）
//   - active 跑完即终态，无 expires_at / 无 Rotator 轮转
//   - active 不入 http_flow 表（自己 spawn striker agent recon，不复用 MITM 代理流量）
//   - 可并发多个 active scan（不受唯一约束限制）
//
// 短期工作笔记 notes 在 Redis（internal/notes 包），按 (scan_id, host) 切分。
package activescan

import "time"

// Status 表示 active scan 的生命周期状态。
type Status string

const (
	StatusActive   Status = "active"
	StatusAborted Status = "aborted"
)

// Scan 是 active_scan 表行的 Go 表示。
//
// 0042 删除冗余 *_count 列：读路径直接查附属表。
type Scan struct {
	ID           string
	Brief        string // 用户原始自然语言任务简报
	TargetHost   string // 可空——某些 brief 不绑单 host
	Status       Status
	CreatedAt    time.Time
	EndedAt      *time.Time
	ErrorMessage string
}
