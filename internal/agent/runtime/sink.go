// Package runtime 承载 ReAct 循环与 Observation 观察点抽象。
//
// Plan 5 阶段：Sink 仅做结构化日志（LoggingSink），不写 graph。
// Plan 6 将加入 GraphSink 将 final_answer 落库到 graph_node。
package runtime

import (
	"context"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

// Observation 是 ReAct 循环每一步对外的事件。
type Observation struct {
	EngagementID uuid.UUID
	TaskID       uuid.UUID
	Role         string
	Kind         string // "final_answer" | "tool_call_deferred"
	Content      string
	ToolName     string // Kind=="tool_call_deferred" 时填
	Step         int
}

// Sink 是 ReAct 循环对外的事件接收器。Plan 5 默认 LoggingSink；Plan 6 替换为 GraphSink。
type Sink interface {
	Observe(ctx context.Context, o Observation) error
}

// LoggingSink 只打结构化日志，不落库；Plan 5 骨架专用。
type LoggingSink struct {
	log zerolog.Logger
}

// NewLoggingSink 构造一个 LoggingSink。
func NewLoggingSink(log zerolog.Logger) *LoggingSink {
	return &LoggingSink{log: log}
}

// Observe 实现 Sink。
func (s *LoggingSink) Observe(_ context.Context, o Observation) error {
	s.log.Info().
		Str("engagement_id", o.EngagementID.String()).
		Str("task_id", o.TaskID.String()).
		Str("role", o.Role).
		Str("kind", o.Kind).
		Str("tool_name", o.ToolName).
		Int("step", o.Step).
		Str("content", o.Content).
		Msg("react observation (dry-run)")
	return nil
}
