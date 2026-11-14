// Package task 实现扫描任务（task 表）的 model + store。
//
// 业务定位：一次扫描活动 = 一个会话。
//   - 任务目标（goal）是智能体看到的目标全文，与探索图根目标的描述同事务写入；
//     target_host 是目标的派生列（目标里第一个 http(s) 目标的主机），随目标一同写入。
//
// 粒度与归属：
//   - 每批流量/每次下发产一个独立 task，同 host 可多个。「是否在监控」从 proxy_traffic 派生。
//   - 附属表（agent_run/finding/...）直接以 task_id 外键挂 task.id。
//
// 状态机：active → completed（自然跑完）| aborted（用户停/取消/错误/心跳超时）。
// FollowUp 可 Reopen 终态 task 续跑（累计停顿进 paused_ms）。
package task

import (
	"regexp"
	"time"
)

// Status 表示 task 的生命周期状态。
type Status string

// Task 状态机：active（进行中）→ completed（planner run 自然跑完）
// 或 aborted（用户停止 / run 超时 / 执行出错 / 恢复监控重投达上限 / 代理任务无流量可领）。
// runner 停机中断不是终态：任务保持 active，由恢复监控以新 run 续跑。暂停是 active 的
// 子状态（paused 标记），不改 status。
const (
	StatusActive    Status = "active"
	StatusCompleted Status = "completed" // planner run 自然跑完的终态
	StatusAborted   Status = "aborted"   // 外部终止或无法继续的终态（见上）
)

// Task 是 task 表行的 Go 表示。
//
// Goal：任务目标（见包注释），可在运行中调整。
// TargetHost：目标的派生列，可空。
// PausedMs：FollowUp 历次停顿累计 ms；WallclockMs 减去它 = 纯 agent 耗时。
type Task struct {
	Paused       bool
	ID           string
	AssignmentID string // 所属 assignment（NOT NULL 强外键，一切 task 皆属某 assignment）
	Goal         string
	TargetHost   string
	Status       Status
	HeartbeatAt  time.Time
	PausedMs     int64
	CreatedAt    time.Time
	EndedAt      *time.Time
	ErrorMessage string
}

// NewParams 是 Store.Create 的入参：AssignmentID 必填（一切 task 皆属某 assignment），
// Goal 必填。
type NewParams struct {
	AssignmentID string
	Goal         string
}

// MaxGoalRunes 是任务目标的长度契约。目标逐字进 executor 与 planner 的提示词，压缩机制
// 不裁它——超大目标绕过全部预算防线，只能在执行期被长度哨兵拦下。契约在入口拒绝而非
// 截断：任务指令被静默截断会腐蚀意图（agent 照半截指令干活）。
const MaxGoalRunes = 20000

// hostRe 匹配目标里的 http(s) 目标（含端口）。
var hostRe = regexp.MustCompile(`https?://([^/\s]+)`)

// targetHostOf 取目标里第一个 http(s) 目标的主机（含端口）；没有则为空。
func targetHostOf(goal string) string {
	if m := hostRe.FindStringSubmatch(goal); m != nil {
		return m[1]
	}
	return ""
}
