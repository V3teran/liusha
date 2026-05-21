// Package owner 定义 owner_type 列的命名常量，替代散布在各包的字符串文字。
//
// owner_type 是 DB CHECK 约束的枚举：所有 polymorphic 表（agent_task / finding /
// llm_invocation / audit_log）的 owner_type 列只允许这两个值。每加一个 owner
// 类型都要同步 DB migration + 此处常量。
package owner

const (
	// Passive 对应 passive_session 表（流量驱动单 agent 任务）。
	Passive = "passive_session"
	// Active 对应 active_scan 表（自然语言 brief 驱动的多 agent swarm 任务）。
	Active = "active_scan"
)
