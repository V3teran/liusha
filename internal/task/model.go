// Package task 实现扫描任务（task 表）的 model + store。
//
// 业务定位：任务是一个目标的测试，探索图、漏洞、运行、LLM / 工具调用都按 task_id 挂在它名下；
// 对话是人与任务交互的通道，一个任务至多一个对话（对话删除后任务仍在）。
//   - 任务目标（goal）是智能体看到的目标全文，与探索图根目标的描述同事务写入；
//     target_host 是目标的派生列（目标里第一个 http(s) 目标的主机），随目标一同写入。
//
// 粒度与归属：
//   - 每批流量/每次下发产一个独立 task，同 host 可多个。「是否在监控」从 proxy_traffic 派生。
//   - 附属表（agent_run/finding/...）直接以 task_id 外键挂 task.id。
//
// 状态机：active → completed（自然跑完）| aborted（用户停/取消/错误/恢复重投达上限）。
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
// PausedMs：续接前的历次停顿累计 ms（结束到再次续接之间的等待），Elapsed 扣除它。
type Task struct {
	Paused       bool
	ID           string
	AssignmentID string // 所属 assignment（NOT NULL 强外键，一切 task 皆属某 assignment）
	Goal         string
	TargetHost   string
	Status       Status
	PausedMs     int64
	CreatedAt    time.Time
	EndedAt      *time.Time
	ErrorMessage string
	Scenario     *Scenario // 建任务时的场景快照（nil = 无场景）；建后不改
}

// Scenario 是建任务时冻结到任务行的场景（code + 当时的名称与正文）：每个 run 都读它，
// 场景之后被改、停用、删除都不影响本任务。
type Scenario struct {
	Code string
	Name string
	Body string
}

// Elapsed 返回任务的耗时：发起到结束（进行中算到 now）的墙钟，扣除续接前的停顿。
func (t Task) Elapsed(now time.Time) time.Duration {
	end := now
	if t.EndedAt != nil {
		end = *t.EndedAt
	}
	d := end.Sub(t.CreatedAt) - time.Duration(t.PausedMs)*time.Millisecond
	if d < 0 {
		return 0
	}
	return d
}

// NewParams 是 Store.Create 的入参：AssignmentID 必填（一切 task 皆属某 assignment），
// Goal 必填；Scenario 可空（无场景）。
type NewParams struct {
	AssignmentID string
	Goal         string
	Scenario     *Scenario
}

// MaxGoalRunes 是任务目标的长度契约。目标逐字进 executor 与 planner 的提示词，压缩机制
// 不裁它——超大目标绕过全部预算防线，只能在执行期被长度哨兵拦下。契约在入口拒绝而非
// 截断：任务指令被静默截断会腐蚀意图（agent 照半截指令干活）。
const MaxGoalRunes = 20000

var (
	// schemeHostRe 匹配目标里带 scheme 的 URL 主机（含端口）——首选派生源。
	schemeHostRe = regexp.MustCompile(`https?://([^/\s]+)`)
	// bareIPv4Re 匹配裸 IPv4（端口可选）：自由文本里的 IPv4 几乎只指目标主机。
	bareIPv4Re = regexp.MustCompile(`\b((?:\d{1,3}\.){3}\d{1,3})(:\d{1,5})?`)
	// bareHostRe 匹配裸主机名（必须含点且带端口）：不带端口或不含点的主机名与散文
	// （「时长 1:30」「v2:8080」这类）歧义过高，不入派生。
	bareHostRe = regexp.MustCompile(`\b([A-Za-z0-9][A-Za-z0-9.-]*\.[A-Za-z0-9-]+:\d{1,5})`)
)

// targetHostOf 取目标里第一个目标地址的主机（含端口）：URL 优先，其次裸地址
// （裸 IPv4 可不带端口，裸主机名须含点带端口）；没有则为空。裸地址是下发 brief 的
// 常见写法（跑分适配器只拿得到 host:port），不认会令 target_host 全空——它是坐实
// 交付时主机线索落空的最终兜底。
func targetHostOf(goal string) string {
	if m := schemeHostRe.FindStringSubmatch(goal); m != nil {
		return m[1]
	}
	if m := bareIPv4Re.FindStringSubmatch(goal); m != nil {
		return m[1] + m[2]
	}
	if m := bareHostRe.FindStringSubmatch(goal); m != nil {
		return m[1]
	}
	return ""
}
