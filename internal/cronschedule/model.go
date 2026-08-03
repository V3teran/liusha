// Package cronschedule 实现定时模板（见 spec §3.3）：不带终态的"闹钟"，到点由 cmd/api 的
// Scheduler goroutine克隆出一次性 assignment 去执行——定时模板本身只有启用/停用两态。
//
// payload 复用 assignment.Item（触发时原样克隆进新 assignment 的清单），
// scenario_id 标识触发场景（克隆进新 assignment 时原样带过去）。
package cronschedule

import (
	"time"

	"github.com/V3teran/liusha/internal/assignment"
)

// CronSchedule 是 cron_schedule 表行的 Go 表示。
type CronSchedule struct {
	ID         string
	ScenarioID string
	CronExpr   string // 标准 5 段 cron（分 时 日 月 周）
	Payload    []byte // jsonb 原文（[]assignment.Item 的序列化，触发时克隆进新 assignment）
	Title      string
	Enabled    bool
	NextRunAt  *time.Time
	LastRunAt  *time.Time
	CreatedAt  time.Time
}

// NewParams 是 Store.Create 的入参。
type NewParams struct {
	ScenarioID string
	CronExpr   string
	Items      []assignment.Item
	Title      string
}
