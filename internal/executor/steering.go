package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/V3teran/liusha/internal/provider"
)

// ActionMetadata 是 action 节点的 metadata 结构（与 planner 保持一致）。
type ActionMetadata struct {
	SteeringMessages []SteeringMessage `json:"steering_messages,omitempty"`
	KilledReason     *KilledReason     `json:"killed_reason,omitempty"`
}

// SteeringMessage 是纠偏消息。
type SteeringMessage struct {
	Timestamp time.Time `json:"timestamp"`
	Source    string    `json:"source"` // "planner" | "self"
	Guidance  string    `json:"guidance"`
	Applied   bool      `json:"applied"` // 是否已应用
}

// KilledReason 是 Kill 原因。
type KilledReason struct {
	Timestamp time.Time `json:"timestamp"`
	Source    string    `json:"source"` // "planner" | "actor"
	Reason    string    `json:"reason"`
}

// applySteeringMessages 读取 worldmodel 中的 steering 消息并注入到对话历史。
func (a *Executor) applySteeringMessages(ctx context.Context, actionID string, messages []provider.Message) []provider.Message {
	// 如果没有 worldmodel 访问权限，跳过
	if a.worldmodel == nil {
		return messages
	}

	// 读取 action 节点
	node, err := a.worldmodel.GetNode(ctx, actionID)
	if err != nil {
		a.logger.Warn().Err(err).Str("action_id", actionID).Msg("failed to read steering messages from worldmodel")
		return messages
	}

	// 解析 metadata
	if len(node.Metadata) == 0 {
		return messages
	}

	var metadata ActionMetadata
	if err := json.Unmarshal(node.Metadata, &metadata); err != nil {
		a.logger.Warn().Err(err).Str("action_id", actionID).Msg("failed to parse action metadata")
		return messages
	}

	// 注入所有未应用的 steering 消息
	appliedCount := 0
	for _, msg := range metadata.SteeringMessages {
		if !msg.Applied {
			messages = append(messages, provider.Message{
				Role:    "user",
				Content: fmt.Sprintf("[STEERING from %s at %s] %s", msg.Source, msg.Timestamp.Format("15:04:05"), msg.Guidance),
			})
			appliedCount++
		}
	}

	if appliedCount > 0 {
		a.logger.Info().
			Str("action_id", actionID).
			Int("count", appliedCount).
			Msg("applied steering messages from worldmodel")
	}

	return messages
}
