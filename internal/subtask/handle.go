// Package subtask 实现 active commander spawn striker（subtask swarm）的运行时。
//
// 拓扑（与 asynq 并列的次级任务通道）：
//
//	commander (asynq → handleActive → react.Run)
//	  ↓ 调 spawn_striker 工具
//	subtask.Spawner.Spawn
//	  ├─ hunter.Create(commander_id=commanderTID) → PG 落 pending 行
//	  ├─ registry.Register → commander 进程内 Handle 句柄
//	  └─ go func() { react.Run(strikerCtx) } → striker 在 commander goroutine 树内跑
//	striker 完成 → handle.MarkDone(outcome) + hunter.SetDone
//
// 共享：sandbox 容器 /  owner 黑板（notes/findings/lessons）— commander 与 striker 同 (eid, host)。
// 隔离：striker ctx 由commander ctx WithCancel 派生（commander abort 自动级联）；striker 有独立 LLM context / inspector。
package subtask

import (
	"sync"
	"time"
)

// ChildStatus 是 Handle 的状态枚举（暴露给 list_strikers 工具）。
type ChildStatus string

const (
	StatusRunning ChildStatus = "running"
	StatusDone    ChildStatus = "done"
	StatusFailed  ChildStatus = "failed"
)

// ChildSnapshot 是 list_strikers 工具看到的striker只读视图。
//
// 字段最小化：commander LLM 只需要知道"striker 在跑什么 / 进展到哪 / 完了没"——
// 详细 finding 走共享黑板（commander read_findings 自然看到）。
type ChildSnapshot struct {
	HunterID      string      `json:"hunter_id"`
	Brief         string      `json:"brief"`
	Status        ChildStatus `json:"status"`
	TotalSteps    int         `json:"total_steps,omitempty"`    // done / failed 时填
	TerminateBy   string      `json:"terminate_by,omitempty"`   // done 时填
	FailureReason string      `json:"failure_reason,omitempty"` // failed 时填
	SpawnedAt     time.Time   `json:"spawned_at"`
	FinishedAt    time.Time   `json:"finished_at,omitempty"` // 终态时填
}

// Outcome 是 striker react.Run 的最小完成信息。
// 与 internal/react.Outcome 解耦——subtask 只需 2 字段，不依赖 react 内部的 Usage/Hints 等。
type Outcome struct {
	TerminateBy string
	TotalSteps  int
}

// Handle 是单个striker的内存句柄（commander 进程持有）。
//
// 线程安全：MarkDone / MarkFailed 由 spawner goroutine 调用，Snapshot 由commander LLM
// 工具调用线程读，mu 保护并发。
type Handle struct {
	hunterID  string
	brief     string
	spawnedAt time.Time

	mu         sync.Mutex
	status     ChildStatus
	outcome    Outcome
	err        error
	finishedAt time.Time
}

// newHandle 由 Registry 内部调用，HunterID + Brief 不可变。
func newHandle(hunterID, brief string) *Handle {
	return &Handle{
		hunterID:  hunterID,
		brief:     brief,
		spawnedAt: time.Now(),
		status:    StatusRunning,
	}
}

// HunterID 返回striker id（不可变）。
func (h *Handle) HunterID() string { return h.hunterID }

// MarkDone 标记striker成功完成。重复调用安全（保留首次状态）。
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

// MarkFailed 标记striker失败（react.Run 报错 / panic recover）。
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
		HunterID:  h.hunterID,
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
