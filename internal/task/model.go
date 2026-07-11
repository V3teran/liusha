// Package task 实现统一扫描任务的 model + store（合并原 activescan + passivesession）。
//
// 业务定位：一次扫描活 = 一个对话。mode 区分主动/被动：
//   - active：用户自然语言 brief 主动下发的一次性站点扫描（brief 是核心，target_host 可空）。
//   - passive：对某 host 一批捕获流量的一次分析（target_host 必填=被分析 host）。
//
// 相较旧双表的语义变化（见 docs/superpowers/specs/2026-07-05-assignment-task-lead-design.md §2）：
//   - passive 不再是「常驻监控会话」（无 expires_at / Rotator / 单 host 唯一约束）——
//     每批流量产一个独立 passive task，同 host 可多个。「是否在监控」从 proxy_traffic 派生。
//   - owner_type + owner_id 多态坍缩为 task_id：附属表（hunter/finding/...）直接挂 task.id。
//
// 状态机：active → completed（自然跑完）| aborted（用户停/取消/错误/心跳超时）。
// active 模式 FollowUp 可 Reopen 终态 task 续跑（累计停顿进 paused_ms）。
package task

import "time"

// Mode 区分主动/被动扫描。
type Mode string

const (
	ModeActive  Mode = "active"
	ModePassive Mode = "passive"
)

// Status 表示 task 的生命周期状态。archived 不再使用（旧 passive_session 遗留，已废弃）。
type Status string

const (
	StatusActive    Status = "active"
	StatusCompleted Status = "completed" // orchestrator run 自然跑完的终态
	StatusAborted   Status = "aborted"   // 用户主动停 / ctx 取消 / 错误 / 心跳超时
)

// Task 是 task 表行的 Go 表示。
//
// Brief：active 用户 brief；passive 为空。
// TargetHost：active 可空；passive 必填（被分析 host）。
// PausedMs：FollowUp 历次停顿累计 ms（passive 恒 0）；WallclockMs 减去它 = 纯 agent 耗时。
type Task struct {
	ID           string
	Mode         Mode
	AssignmentID string // 所属 assignment（NOT NULL 强外键，一切 task 皆属某 assignment）
	Brief        string
	TargetHost   string
	Status       Status
	HeartbeatAt  time.Time
	PausedMs     int64
	CreatedAt    time.Time
	EndedAt      *time.Time
	ErrorMessage string
}

// NewParams 是 Store.Create 的入参。
//   - AssignmentID 必填（一切 task 皆属某 assignment，§3 "一切皆 assignment"）。
//   - active：Brief 必填，TargetHost 可空（scanner 从 brief 抽到后 SetTargetHost 回填）。
//   - passive：TargetHost 必填，Brief 留空。
type NewParams struct {
	Mode         Mode
	AssignmentID string
	Brief        string
	TargetHost   string
}
