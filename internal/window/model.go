// Package window 实现 traffic_window 聚合：把流量按批次切窗，
// 状态机 open → closed → consumed，供下游 BAC pipeline 拉取分析。
package window

import "time"

// Status 是 traffic_window.status 的取值。
type Status string

const (
	StatusOpen     Status = "open"
	StatusClosed   Status = "closed"
	StatusConsumed Status = "consumed"
)

// FlowRef 是 flows jsonb 数组里的一项，引用 traffic_flow 表。
// 仅保留下游分析必需的最小字段（id 必填，method/url 可选用于日志/调试）。
type FlowRef struct {
	ID     int64  `json:"id"`
	Method string `json:"method,omitempty"`
	URL    string `json:"url,omitempty"`
}

// Window 是 traffic_window 表行的 Go 表示。
type Window struct {
	ID           string
	EngagementID string
	Flows        []FlowRef
	Status       Status
	StartedAt    time.Time
	ClosedAt     *time.Time
}
