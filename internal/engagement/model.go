// Package engagement 实现 engagement 聚合根的 model 与 store。
// engagement 是一次"扫描会话"，按 (tenant, target_host) 懒创建；active 唯一。
// memory_notes（kind=observation/hypothesis/boundary）作为 engagement-scope 状态板。
package engagement

import (
	"encoding/json"
	"time"
)

// Mode 表示 engagement 的工作模式。
type Mode string

// Status 表示 engagement 的生命周期状态。
type Status string

// notes v0024 起为纯自由文本（删 kind enum）；写入由
// internal/tools/common/memory.go 的 write_memory 工具完成。
const (
	ModeProxy   Mode = "proxy"
	ModeBrowser Mode = "browser"

	StatusActive   Status = "active"
	StatusAborted  Status = "aborted"
	StatusArchived Status = "archived"
)

// Engagement 是 engagement 表行的 Go 表示。
//
// v0010：删除 LastActivityAt 字段（Touch() 无人调用、仅 Abort 时设一次但无下游消费者）。
// v0015：加 EndedAt / ErrorMessage / *Count 字段（借鉴 liusha2 task 表的进度统计）。
//   - EndedAt 仅 Abort 时填；active 状态保持 nil。
//   - *Count 字段 active 期间由 vulnfinding/flow/reactrun 写路径 best-effort 增量；
//     Abort 时事务内 SELECT count(*) 重算精确兜底。
type Engagement struct {
	ID            string
	TenantID      string
	Mode          Mode
	TargetHost     string
	Status        Status
	MemoryNotes   []byte // jsonb: {notes: [{kind, content, status?, agent_run_id, scope}]}
	CreatedAt     time.Time
	EndedAt       *time.Time
	ErrorMessage  string
	FlowCount     int
	FindingCount  int
	AgentRunCount int
}

// State 是 ReadState action 返回的视图。
//
// notes 单层（kind enum 区分 observation/hypothesis/boundary）。
type State struct {
	Notes json.RawMessage `json:"notes"`
}

// ReadOpts 是 Store.ReadStateScoped 的可选过滤/截断参数。
//
// TaskID 非空时不强过滤 entry——所有 engagement-scope 的 notes 全返（跨 task 共享）；
// done_validator 凭 entry 的 task_id 字段自行判定是否本 task 写过。
//
// NotesLimit：
//   - 0 → 用默认（100）
//   - > 0 → 取末尾 N 条
//   - < 0 → 不截断
type ReadOpts struct {
	TaskID     string
	NotesLimit int
}
