// Package task 实现统一扫描任务的 model + store（合并原 activescan + passivesession）。
//
// 业务定位：一次扫描活 = 一个会话。scenario_id 标识所属场景（配置驱动，应用层校验）：
//   - 输入统一为一段 brief 文本（用户自然语言目标描述）；target_host 为派生列，
//     由 runner 从 brief 抽取后 SetTargetHost 回填，非用户输入的第二形态。
//   - 具体引擎（solo/swarm）由 scenario 配置决定，task 只记 scenario_id，不区分主被动。
//
// 相较旧双表的语义变化（见 docs/superpowers/specs/2026-07-05-assignment-task-lead-design.md §2）：
//   - 每批流量/每次下发产一个独立 task，同 host 可多个。「是否在监控」从 proxy_traffic 派生。
//   - owner_type + owner_id 多态坍缩为 task_id：附属表（hunter/finding/...）直接挂 task.id。
//
// 状态机：active → completed（自然跑完）| aborted（用户停/取消/错误/心跳超时）。
// FollowUp 可 Reopen 终态 task 续跑（累计停顿进 paused_ms）。
package task

import "time"

// Status 表示 task 的生命周期状态。archived 不再使用（旧 passive_session 遗留，已废弃）。
type Status string

const (
	StatusActive    Status = "active"
	StatusCompleted Status = "completed" // orchestrator run 自然跑完的终态
	StatusAborted   Status = "aborted"   // 用户主动停 / ctx 取消 / 错误 / 心跳超时
)

// Task 是 task 表行的 Go 表示。
//
// ScenarioID：所属场景 code（配置驱动，标识引擎与 playbook）。
// Brief：用户自然语言目标描述（统一输入）。
// TargetHost：派生列，runner 从 brief 抽取后回填，可空。
// PausedMs：FollowUp 历次停顿累计 ms；WallclockMs 减去它 = 纯 agent 耗时。
type Task struct {
	ID           string
	ScenarioID   string
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
//   - ScenarioID 必填（标识所属场景）。
//   - Brief 必填（统一输入）；TargetHost 可空（runner 从 brief 抽到后 SetTargetHost 回填）。
type NewParams struct {
	ScenarioID   string
	AssignmentID string
	Brief        string
	TargetHost   string
}
