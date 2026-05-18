// Package subtask 实现 active hunter 父任务 spawn 子任务（subtask swarm）的运行时。
//
// 拓扑（与 asynq 并列的次级任务通道）：
//
//	父 active hunter (asynq → handleActive → react.Run)
//	  ↓ 调 spawn_child 工具
//	subtask.Spawner.Spawn
//	  ├─ agentrun.Create(parent_id=父TID) → PG 落 pending 行
//	  ├─ registry.Register → 父进程内 Handle 句柄
//	  └─ go func() { react.Run(childCtx) } → 子在父 goroutine 树内跑
//	子完成 → handle.MarkDone(outcome) + agentrun.SetDone
//
// 共享：sandbox 容器 / engagement 黑板（notes/findings/lessons）— 父子同 (eid, host)。
// 隔离：子 ctx 由父 ctx WithCancel 派生（父 abort 自动级联）；子有独立 LLM context / reviewer。
package subtask

import (
	"sync"
	"time"
)

// ChildStatus 是 Handle 的状态枚举（暴露给 list_children 工具）。
type ChildStatus string

const (
	StatusRunning ChildStatus = "running"
	StatusDone    ChildStatus = "done"
	StatusFailed  ChildStatus = "failed"
)

// ChildSnapshot 是 list_children 工具看到的子任务只读视图。
//
// 字段最小化：父 LLM 只需要知道"子在跑什么 / 进展到哪 / 完了没"——
// 详细 finding 走共享黑板（父 read_findings 自然看到）。
type ChildSnapshot struct {
	TaskID        string      `json:"task_id"`
	Brief         string      `json:"brief"`
	Status        ChildStatus `json:"status"`
	TotalSteps    int         `json:"total_steps,omitempty"`    // done / failed 时填
	TerminateBy   string      `json:"terminate_by,omitempty"`   // done 时填
	FailureReason string      `json:"failure_reason,omitempty"` // failed 时填
	SpawnedAt     time.Time   `json:"spawned_at"`
	FinishedAt    time.Time   `json:"finished_at,omitempty"` // 终态时填
}

// Outcome 是子 react.Run 的最小完成信息。
// 与 internal/react.Outcome 解耦——subtask 包不 import react，避免循环依赖。
type Outcome struct {
	TerminateBy string
	TotalSteps  int
}

// Handle 是单个子任务的内存句柄（父进程持有）。
//
// 线程安全：MarkDone / MarkFailed 由 spawner goroutine 调用，Snapshot 由父 LLM
// 工具调用线程读，mu 保护并发。
type Handle struct {
	taskID    string
	brief     string
	spawnedAt time.Time

	mu         sync.Mutex
	status     ChildStatus
	outcome    Outcome
	err        error
	finishedAt time.Time
}

// newHandle 由 Registry 内部调用，TaskID + Brief 不可变。
func newHandle(taskID, brief string) *Handle {
	return &Handle{
		taskID:    taskID,
		brief:     brief,
		spawnedAt: time.Now(),
		status:    StatusRunning,
	}
}

// TaskID 返回子任务 id（不可变）。
func (h *Handle) TaskID() string { return h.taskID }

// MarkDone 标记子任务成功完成。重复调用安全（保留首次状态）。
func (h *Handle) MarkDone(o Outcome) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.status != StatusRunning {
		return
	}
	h.status = StatusDone
	h.outcome = o
	h.finishedAt = time.Now()
}

// MarkFailed 标记子任务失败（react.Run 报错 / panic recover）。
func (h *Handle) MarkFailed(err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.status != StatusRunning {
		return
	}
	h.status = StatusFailed
	h.err = err
	h.finishedAt = time.Now()
}

// IsRunning 检查是否仍在 running 状态（done PreCheck 用）。
func (h *Handle) IsRunning() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.status == StatusRunning
}

// Snapshot 返回不可变快照。
func (h *Handle) Snapshot() ChildSnapshot {
	h.mu.Lock()
	defer h.mu.Unlock()
	snap := ChildSnapshot{
		TaskID:    h.taskID,
		Brief:     h.brief,
		Status:    h.status,
		SpawnedAt: h.spawnedAt,
	}
	switch h.status {
	case StatusDone:
		snap.TotalSteps = h.outcome.TotalSteps
		snap.TerminateBy = h.outcome.TerminateBy
		snap.FinishedAt = h.finishedAt
	case StatusFailed:
		snap.TotalSteps = h.outcome.TotalSteps
		if h.err != nil {
			snap.FailureReason = h.err.Error()
		}
		snap.FinishedAt = h.finishedAt
	}
	return snap
}
