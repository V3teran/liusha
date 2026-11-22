// Package overview 是「概览」页的只读聚合：从任务、发现、LLM 调用三张业务表直接统计
// 运行中的工作、待处理的发现、近期产出与消耗、各目标的暴露面。不落任何派生表——
// 数字与各明细页同一口径（待处理 = 发现状态 open，LLM 错误 = error_message 非空）。
package overview

import (
	"time"

	"github.com/V3teran/liusha/internal/finding"
)

// DefaultWindowDays 是时间窗的默认值（按天，含今天）；OverviewPage 可在 7 / 14 / 30 天间切换。
const DefaultWindowDays = 14

// AllowedWindowDays 是允许的时间窗取值集合；界面上的切换档位与之对应。
var AllowedWindowDays = map[int]bool{7: true, 14: true, 30: true}

// TargetLimit 是目标排行返回的条数上限。
const TargetLimit = 8

// Severities 是发现严重级的固定顺序（由重到轻），也是按天序列与排行里各计数的键；取自台账的五档。
var Severities = finding.Severities

// Query 是一次快照的参数。Location 决定「一天」的边界——按天统计必须按查看者所在时区切分，
// 否则服务器时区与查看者的「今天」错开，当天的数字落进昨天。
type Query struct {
	Now      time.Time
	Location *time.Location
	// Days 是趋势时间窗的天数（AllowedWindowDays 之一），由 HTTP 层校验。
	Days int
}

// Snapshot 是概览快照。
type Snapshot struct {
	GeneratedAt time.Time `json:"generated_at"`
	Window      Window    `json:"window"`
	Tasks       Tasks     `json:"tasks"`
	Findings    Findings  `json:"findings"`
	Usage       Usage     `json:"usage"`
	Targets     []Target  `json:"targets"`
}

// Window 描述时间窗：从 Since（查看者时区的零点）起的 Days 天。
type Window struct {
	Days  int       `json:"days"`
	Since time.Time `json:"since"`
}

// Tasks 是进行中的任务计数：运行与暂停。
type Tasks struct {
	Running int `json:"running"`
	Paused  int `json:"paused"`
}

// SeverityCounts 是按严重级的计数（键取 Severities，缺的级别为 0）。
type SeverityCounts map[string]int

// Findings 是发现统计：待处理按严重级；按天的新增按严重级；全部发现按类型。
type Findings struct {
	Open    SeverityCounts  `json:"open"`
	Daily   []DailySeverity `json:"daily"`
	Classes []ClassCount    `json:"classes"`
}

// ClassCount 是一种发现类型的发现数：全部与其中待处理的。按全部发现数降序；类型取自台账的
// 封闭词表，种类有限，全部返回。
type ClassCount struct {
	Class string `json:"class"`
	Name  string `json:"name"` // 展示名（finding.Class）
	Total int    `json:"total"`
	Open  int    `json:"open"`
}

// DailySeverity 是某天新增的发现按严重级计数。
type DailySeverity struct {
	Date   string         `json:"date"` // 查看者时区的日期 YYYY-MM-DD
	Counts SeverityCounts `json:"counts"`
}

// Usage 是 LLM 消耗：按天序列与最近 24 小时汇总。
type Usage struct {
	Daily   []DailyUsage `json:"daily"`
	Last24h UsageTotals  `json:"last_24h"`
}

// UsageTotals 是一段时间内的 LLM 调用汇总。
type UsageTotals struct {
	Calls        int   `json:"calls"`
	Errors       int   `json:"errors"`
	InTokens     int64 `json:"in_tokens"`
	OutTokens    int64 `json:"out_tokens"`
	CachedTokens int64 `json:"cached_tokens"`
}

// DailyUsage 是某天的 LLM 调用汇总。
type DailyUsage struct {
	Date string `json:"date"`
	UsageTotals
}

// Target 是一个目标（任务的目标主机）的暴露面：任务数、进行中的任务数、待处理发现按严重级、
// 发现总数与最近活动时间（最近一次建任务或产出发现）。
type Target struct {
	Host           string         `json:"host"`
	Tasks          int            `json:"tasks"`
	ActiveTasks    int            `json:"active_tasks"`
	Open           SeverityCounts `json:"open"`
	Findings       int            `json:"findings"`
	LastActivityAt time.Time      `json:"last_activity_at"`
}
