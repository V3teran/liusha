// Package agentrun 实现 agent_run 持久化层：每次 ReAct 调用对应一行，
// 状态机 pending → running → done | error | aborted。
//
// 父子关系（subtask swarm）：
//   - 父 / 独立任务：parent_id = NULL（Go 层 ParentID = ""）
//   - 子任务：parent_id 指向父 agent_run.id；子任务**不**入 asynq，
//     由 internal/subtask 包在父 goroutine 内手动调 Store.Create 写入。
//     list_children 工具从 subtask.Registry 内存读，不查 PG（PG parent_id 列只供
//     viewer 树渲染 + ListByEngagement 一并取父子）。
//   - **崩溃恢复语义**：scanner 进程重启后，PG `parent_id` 列仅供展示——内存
//     Registry 已丢，list_children 工具看不到崩溃前的子；active 父走 asynq
//     MaxRetry(0) 不再重试，handle 入口 GetByID 见 status != pending 直接 SkipRetry。
//     所以崩溃后父子树在 viewer 仍可见，但运行时父子关系不可从 PG 恢复。
package agentrun

import (
	"encoding/json"
	"time"
)

// Status 是 agent_task.status 的取值。
type Status string

const (
	StatusPending Status = "pending"
	StatusRunning Status = "running"
	StatusDone    Status = "done"
	StatusAborted Status = "aborted"
	StatusError   Status = "error"
)

// ReactRun 是 agent_run 表行的 Go 表示。Result 在终态前为空 jsonb '{}'。
// ParentID 空表示独立/根任务；非空时指向父 agent_run.id（subtask swarm）。
//
// 双轨期：EngagementID（旧）与 OwnerType+OwnerID（新 polymorphic）并存。
// 0038 之后 caller 可写双轨，commit B5 之后 DROP engagement_id。
type ReactRun struct {
	ID           string
	EngagementID string // 旧字段，待 DROP（commit B5）
	OwnerType    string // 新字段，'passive_session' 或 'active_scan'（空 = 旧路径）
	OwnerID      string // 新字段，passive_session.id / active_scan.id（空 = 旧路径）
	ParentID     string
	Role         string
	Input        json.RawMessage
	Result       json.RawMessage
	Status       Status
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// NewParams 是 Store.Create 的入参。
// ParentID 留空表示独立/根任务；填值时 INSERT 写入 parent_id 列（subtask 用）。
//
// 双轨：EngagementID 必填（NOT NULL 列）；OwnerType+OwnerID 可空（commit B 切换后填）。
type NewParams struct {
	EngagementID string
	OwnerType    string // 可空——'passive_session' / 'active_scan'
	OwnerID      string // 可空
	Role         string
	Input        json.RawMessage
	ParentID     string
}
