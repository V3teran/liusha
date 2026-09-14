package core

import (
	"context"

	"github.com/rs/zerolog"
)

// LoggerCallback 是基于 zerolog 的日志 Callback 实现。
type LoggerCallback struct {
	NoopCallback
	logger zerolog.Logger
}

// NewLoggerCallback 创建日志 Callback。
func NewLoggerCallback(logger zerolog.Logger) *LoggerCallback {
	return &LoggerCallback{
		logger: logger.With().Str("component", "callback").Logger(),
	}
}

// OnAgentStart 记录 Agent 启动
func (l *LoggerCallback) OnAgentStart(ctx context.Context, event AgentStartEvent) {
	l.logger.Info().
		Str("agent", event.AgentName).
		Str("task_id", event.TaskID).
		Time("start_time", event.StartTime).
		Msg("agent started")
}

// OnAgentEnd 记录 Agent 结束
func (l *LoggerCallback) OnAgentEnd(ctx context.Context, event AgentEndEvent) {
	logEvent := l.logger.Info()
	if event.Error != nil {
		logEvent = l.logger.Error().Err(event.Error)
	}

	logEvent.
		Str("agent", event.AgentName).
		Str("task_id", event.TaskID).
		Dur("duration", event.Duration).
		Msg("agent ended")
}

// OnToolStart 记录 Tool 执行前
func (l *LoggerCallback) OnToolStart(ctx context.Context, event ToolStartEvent) {
	l.logger.Debug().
		Str("tool", event.ToolName).
		Str("task_id", event.TaskID).
		Time("start_time", event.StartTime).
		Msg("tool started")
}

// OnToolEnd 记录 Tool 执行后
func (l *LoggerCallback) OnToolEnd(ctx context.Context, event ToolEndEvent) {
	logEvent := l.logger.Debug()
	if event.Error != nil {
		logEvent = l.logger.Warn().Err(event.Error)
	}

	logEvent.
		Str("tool", event.ToolName).
		Str("task_id", event.TaskID).
		Dur("duration", event.Duration).
		Msg("tool ended")
}

// OnLLMStart 记录 LLM 调用前
func (l *LoggerCallback) OnLLMStart(ctx context.Context, event LLMStartEvent) {
	l.logger.Info().
		Str("provider", event.ProviderID).
		Str("model", event.ModelID).
		Str("task_id", event.TaskID).
		Int("messages", len(event.Request.Messages)).
		Int("tools", len(event.Request.Tools)).
		Msg("llm call started")
}

// OnLLMEnd 记录 LLM 调用后
func (l *LoggerCallback) OnLLMEnd(ctx context.Context, event LLMEndEvent) {
	logEvent := l.logger.Info()
	if event.Error != nil {
		logEvent = l.logger.Error().Err(event.Error)
	}

	logEvent.
		Str("provider", event.ProviderID).
		Str("model", event.ModelID).
		Str("task_id", event.TaskID).
		Dur("duration", event.Duration).
		Int("in_tokens", event.Response.Usage.InTokens).
		Int("out_tokens", event.Response.Usage.OutTokens).
		Int("cached_tokens", event.Response.Usage.CachedTokens).
		Str("finish_reason", event.Response.FinishReason).
		Msg("llm call ended")
}
