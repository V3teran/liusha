package worker

import (
	"context"
	"encoding/json"

	"github.com/hibiken/asynq"
)

// Payload 是所有 agent 任务的统一载荷：只指明跑哪个 run、属于哪个任务。run 不携带输入——
// 任务目标在任务行与探索图上，开跑时读取（续跑、恢复重投、目标调整后都以最新目标为准）。
// 全部生产者（api 组件人工下发 / ingestor 代理捕获攒批 / 恢复监控）都填 AgentRunID + TaskID。
type Payload struct {
	AgentRunID string `json:"agent_run_id"` // 所属 agent_run.id（幂等键，见 client.go）
	TaskID     string `json:"task_id"`      // 所属 task.id
	// ConversationID 关联本任务所属会话（会话发起时填）；asynq 自动入口为空——
	// 空则 runner 不发过程事件、不落 conversation message（纯后台扫描）。
	ConversationID string   `json:"conversation_id,omitempty"`
	TaskRole       TaskRole `json:"role"` // asynq 队列路由角色
}

// RoleHandler 处理一个反序列化好的 Payload。
type RoleHandler func(ctx context.Context, p Payload) error

// Mux 按 TaskRole 路由 asynq 任务到对应 handler；经验沉淀另有自己的任务类型。
type Mux struct {
	handlers map[TaskRole]RoleHandler
	distill  DistillHandler
}

// NewMux 创建一个空的 Mux。
func NewMux() *Mux {
	return &Mux{handlers: make(map[TaskRole]RoleHandler)}
}

// Register 注册 role 对应的 handler。重复注册会覆盖旧值。
func (m *Mux) Register(role TaskRole, h RoleHandler) {
	m.handlers[role] = h
}

// HandleDistill 注册经验沉淀的处理函数。
func (m *Mux) HandleDistill(h DistillHandler) {
	m.distill = h
}

// AsynqMux 返回一个 asynq.ServeMux：
// 把所有 TaskTypeRun 任务反序列化为 Payload，并按 TaskRole 分发到注册的 handler。
// 未注册的 role 或 payload 损坏时返回 asynq.SkipRetry，避免重试风暴。
func (m *Mux) AsynqMux() *asynq.ServeMux {
	mux := asynq.NewServeMux()
	mux.HandleFunc(TaskTypeRun, func(ctx context.Context, t *asynq.Task) error {
		var p Payload
		if err := json.Unmarshal(t.Payload(), &p); err != nil {
			return asynq.SkipRetry
		}
		h, ok := m.handlers[p.TaskRole]
		if !ok {
			return asynq.SkipRetry
		}
		return h(ctx, p)
	})
	if m.distill != nil {
		mux.HandleFunc(TaskTypeDistill, func(ctx context.Context, t *asynq.Task) error {
			var p distillPayload
			if err := json.Unmarshal(t.Payload(), &p); err != nil || p.TaskID == "" {
				return asynq.SkipRetry
			}
			return m.distill(ctx, p.TaskID)
		})
	}
	return mux
}
