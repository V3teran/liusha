// Package assignment 实现下发容器 model + store（见 spec §3）。
//
// 业务定位：一切下发皆走 assignment——api 单发/批量、聚合器自动攒批，后端统一
// "建 assignment → 展开 task"。task.assignment_id NOT NULL 强外键，无孤儿 task。
//
//   - 单发   = 单元素 assignment（1 task）
//   - 批量   = 多元素 assignment（fan-out 多 task）
//   - 聚合   = 一批流量 → 1 task（fan-in）
//
// assignment 无 status 列：整体状态由子 task 聚合派生
// （读时算，见 Store.DeriveStatus），不落存储——避免"assignment 状态与子 task 真实态不一致"的双写难题。
package assignment

import "time"

// Source 标记谁发的：manual 人工（api 下发）/ auto 聚合器（流量自动攒批）。
type Source string

const (
	SourceManual Source = "manual"
	SourceAuto   Source = "auto"
)

// Status 是 assignment 的派生整体状态（不落存储，由子 task 聚合算出）。
type Status string

const (
	StatusEmpty   Status = "empty"   // 无子 task（异常/刚建未展开）
	StatusRunning Status = "running" // 有子 task 处于 active
	StatusDone    Status = "done"    // 全部子 task 终态且至少一个 completed
	StatusAborted Status = "aborted" // 全部子 task 终态且无 completed（全 aborted）
)

// Assignment 是 assignment 表行的 Go 表示。
//
// Payload：下发清单的 jsonb 原文（[]Item 的序列化）。
// ScheduleID：由哪个定时模板克隆而来（手动下发为 nil）。
type Assignment struct {
	ID         string
	Source     Source
	Payload    []byte // jsonb 原文（[]Item 的序列化）
	Title      string
	ScheduleID *string
	CreatedAt  time.Time
}

// Item 是 payload 数组的单个条目。active 用 Brief（+可选 Host）；passive 用 TrafficIDs。
type Item struct {
	Brief      string  `json:"brief,omitempty"`
	Host       string  `json:"host,omitempty"`
	TrafficIDs []int64 `json:"traffic_ids,omitempty"`
}

// NewParams 是 Store.Create 的入参。
type NewParams struct {
	Source     Source
	Items      []Item
	Title      string
	ScheduleID *string // 定时克隆来源；手动下发传 nil
}
