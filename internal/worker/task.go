// Package worker 负责 Asynq 任务类型定义、入队客户端与处理器。
//
// 本文件定义 agent:run 任务的 payload 与构造函数，后续 client / handler 复用这一类型。
package worker

import (
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

// TypeAgentRun 是 Asynq 任务类型名，所有 agent 执行任务走这条队列。
const TypeAgentRun = "agent:run"

// AgentRunPayload 是 agent:run 任务的序列化 payload。
//
// TaskID 同时作为 Asynq Unique key，保证同一 AgentTask 不会被重复入队。
type AgentRunPayload struct {
	EngagementID uuid.UUID   `json:"engagement_id"`
	WindowID     uuid.UUID   `json:"window_id"`
	TaskID       uuid.UUID   `json:"task_id"`
	Role         string      `json:"role"`
	InputNodeIDs []uuid.UUID `json:"input_node_ids,omitempty"`
}

// NewAgentRunTask 构造一个 agent:run 类型的 asynq.Task，payload 为 AgentRunPayload 的 JSON。
func NewAgentRunTask(p AgentRunPayload) (*asynq.Task, error) {
	b, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("序列化 AgentRunPayload 失败: %w", err)
	}
	return asynq.NewTask(TypeAgentRun, b), nil
}
