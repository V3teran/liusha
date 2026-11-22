// Package cronschedule 实现定时模板：不带终态的"闹钟"，到点由 api 组件的
// Scheduler goroutine 克隆出一次性 assignment，按清单每条展开一个任务——定时模板本身只有
// 启用/停用两态。
package cronschedule

import "time"

// Item 是模板清单的一条：到点展开成一个任务的目标描述。
type Item struct {
	Brief string `json:"brief"`
}

// Schedule 是 cron_schedule 表行的 Go 表示。
type Schedule struct {
	ID          string
	CronExpr    string // 标准 5 段 cron（分 时 日 月 周）
	Payload     []byte // jsonb 原文（[]Item 的序列化）
	Title       string // 模板名，也是到点克隆出的批次名
	Description string // 模板描述（可空）
	Scenario    string // 到点建任务所用的场景 code（空 = 无场景）；只引用存在且启用的场景
	Enabled     bool
	NextRunAt   *time.Time
	LastRunAt   *time.Time
	CreatedAt   time.Time
}

// NewParams 是 Store.Create 的入参。
type NewParams struct {
	CronExpr    string
	Items       []Item
	Title       string
	Description string
	Scenario    string // 空 = 无场景；非空须存在且启用（建模板时锁住场景行核对）
}
