// Package passivesession 实现被动监控会话的 model + store。
//
// 业务定位：cmd/proxy MITM 拦截流量驱动的会话——1 个 host 对应 1 个 active session
// （唯一索引 passive_session_active_host_uniq），expires_at = created_at + 24h
// （Rotator 轮转）。流量进来 → LookupOrCreate by host → 入 http_flow → 触发 hunter agent。
//
// 与 active_scan 的差异：
//   - passive 按 host 切（"哪个站点正在被监控"），active 按 brief 切（"哪次任务"）
//   - passive 共享 cmd/proxy 拦截的所有流量；active 自己 spawn 子，不入 http_flow 表
//   - passive expires_at 必填；active 跑完即终态，无 TTL
//
// 短期工作笔记 notes 在 Redis（internal/notes 包），按 (session_id, host) 切分。
package passivesession

import "time"

// Status 表示 passive session 的生命周期状态。
type Status string

const (
	StatusActive   Status = "active"
	StatusAborted  Status = "aborted"
	StatusArchived Status = "archived"
)

// Session 是 passive_session 表行的 Go 表示。
//
// FlowCount / FindingCount / AgentRunCount：增量维护的近似计数，**允许漂移**。
// 运行期 Increment*Count 是 best-effort，失败仅 log warn 不阻塞业务；
// 仅 Abort 路径用 SELECT count(*) 精确兜底落库。
type Session struct {
	ID            string
	Host          string
	Status        Status
	CreatedAt     time.Time
	ExpiresAt     time.Time
	EndedAt       *time.Time
	ErrorMessage  string
	FlowCount     int
	FindingCount  int
	AgentRunCount int
}
