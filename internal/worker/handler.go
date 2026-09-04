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
// plannerID 标识 planner id（旧 subtask swarm 语义）；空表示独立任务/根任务。
// 现行 active 路径用 dispatcher/actor 进程内编排，exploitation 不入 asynq，故入队 Payload 此字段恒空；
// 已删除废弃的 plannerID 和 ScenarioID 字段（v1.5 active 重构后不再使用）。
type Payload struct {
	AgentID        string `json:"agent_id"`
	TaskID         string `json:"task_id"` // 所属 task.id
	// ConversationID 关联本任务所属会话（阶段B 会话发起时填）；asynq 自动入口为空——
	// 空则 runner 不发过程事件、不落 conversation message（纯后台扫描）。
	ConversationID string `json:"conversation_id,omitempty"`
	Role       Role            `json:"role"`
	Input      json.RawMessage `json:"input,omitempty"`
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
