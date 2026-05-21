package worker

import (
	"context"
	"encoding/json"

	"github.com/hibiken/asynq"
)

// Payload 是所有 agent 任务的统一载荷。
//
// Input 是该 task 的入参（已序列化的 JSON），由 handler 自行解释。
//
// CommanderTaskID 标识commander id（subtask swarm）；空表示独立任务/根任务。
// 设计约束：striker永远在commander goroutine 内跑（subtask 包内），**不**入 asynq——
// 因此正常情况下入队 Payload.CommanderTaskID 永远为空；ingestor + httpapi
// enqueue 调用方均不填本字段，scanner handleActive 也不再做 fail-fast 死分支。
// 字段保留用于 internal/subtask 包在commander goroutine 内 BuilderParams 传递。
type Payload struct {
	TaskID       string          `json:"agent_run_id"`
	OwnerType    string          `json:"owner_type"` // 'passive_session' / 'active_scan'
	OwnerID      string          `json:"owner_id"`   // passive_session.id / active_scan.id
	CommanderTaskID string          `json:"commander_task_id,omitempty"`
	Role         Role            `json:"role"`
	Input        json.RawMessage `json:"input,omitempty"`
}

// RoleHandler 处理一个反序列化好的 Payload。
type RoleHandler func(ctx context.Context, p Payload) error

// Mux 按 Role 路由 asynq 任务到对应 handler。
type Mux struct {
	handlers map[Role]RoleHandler
}

// NewMux 创建一个空的 Mux。
func NewMux() *Mux {
	return &Mux{handlers: make(map[Role]RoleHandler)}
}

// Register 注册 role 对应的 handler。重复注册会覆盖旧值。
func (m *Mux) Register(role Role, h RoleHandler) {
	m.handlers[role] = h
}

// AsynqMux 返回一个 asynq.ServeMux：
// 把所有 TaskTypeRun 任务反序列化为 Payload，并按 Role 分发到注册的 handler。
// 未注册的 role 或 payload 损坏时返回 asynq.SkipRetry，避免重试风暴。
func (m *Mux) AsynqMux() *asynq.ServeMux {
	mux := asynq.NewServeMux()
	mux.HandleFunc(TaskTypeRun, func(ctx context.Context, t *asynq.Task) error {
		var p Payload
		if err := json.Unmarshal(t.Payload(), &p); err != nil {
			return asynq.SkipRetry
		}
		h, ok := m.handlers[p.Role]
		if !ok {
			return asynq.SkipRetry
		}
		return h(ctx, p)
	})
	return mux
}
