// timeline.go 工具过程事件：每次工具调用/结果落会话时间线并实时广播——
// 对话框可见「agent 正在对目标做什么」（调用 → 观测），与思考流（在想什么）
// 互补构成完整过程面。错误随 tool_result 内联（err 字段），失败一眼可见。

package cognition

import (
	"context"
	"encoding/json"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/conversation"
	"github.com/V3teran/liusha/internal/registry"
)

// maxToolEventRunes 是时间线事件单字段截断上限（args 与 result 各自适用）：
// 时间线要的是可读过程，完整原文在 tool_invocation（遥测）与证据链里。
const maxToolEventRunes = 4000

// timelineWriter 时间线事件的落库通道（*conversation.Store 满足）。
type timelineWriter interface {
	AppendMessage(ctx context.Context, convID string, role conversation.Role, kind conversation.Kind, content string, metadata json.RawMessage) (conversation.Message, error)
}

// timelinePublisher 时间线事件的 SSE 广播通道（*scanstream.Publisher 满足）。
type timelinePublisher interface {
	Publish(ctx context.Context, conversationID string, payload []byte) error
}

// toolTimelineEvents 把工具过程事件接入会话时间线（落库 + 实时广播）。
type toolTimelineEvents struct {
	writer timelineWriter
	pub    timelinePublisher
	logger zerolog.Logger
}

// newToolTimelineEvents 构造事件出口（writer/pub 任一为 nil 时整体直通，不产生事件）。
func newToolTimelineEvents(w timelineWriter, p timelinePublisher, logger zerolog.Logger) *toolTimelineEvents {
	return &toolTimelineEvents{writer: w, pub: p, logger: logger}
}

// interceptor 返回接入工具执行链的事件拦截器。conversationID 为空（无会话的
// 后台扫描）时直通。
func (e *toolTimelineEvents) interceptor(conversationID string) registry.Interceptor {
	return func(ctx context.Context, t registry.Tool, args []byte, next registry.ExecuteFunc) (registry.ToolResult, error) {
		if conversationID == "" || e.writer == nil || e.pub == nil {
			return next(ctx, t, args)
		}
		agent := AgentFromCtx(ctx)
		e.emit(ctx, conversationID, map[string]any{
			"kind":       "tool_call",
			"agent_name": agent,
			"tool_name":  t.Name(),
			"args":       truncateEventRunes(string(args)),
		})

		start := time.Now()
		res, err := next(ctx, t, args)

		errMsg := ""
		if res.Error != "" {
			errMsg = res.Error
		} else if err != nil {
			errMsg = err.Error()
		}
		e.emit(ctx, conversationID, map[string]any{
			"kind":        "tool_result",
			"agent_name":  agent,
			"tool_name":   t.Name(),
			"result":      truncateEventRunes(res.Output),
			"duration_ms": time.Since(start).Milliseconds(),
			"err":         truncateEventRunes(errMsg),
		})
		return res, err
	}
}

// emit 落一条 agent 过程事件并广播 SSE（best-effort：失败仅告警，工具执行本体
// 不受影响——时间线是观测面，不构成执行依赖）。
func (e *toolTimelineEvents) emit(ctx context.Context, convID string, meta map[string]any) {
	raw, err := json.Marshal(meta)
	if err != nil {
		e.logger.Warn().Err(err).Msg("工具过程事件序列化失败")
		return
	}
	msg, err := e.writer.AppendMessage(ctx, convID,
		conversation.RoleAssistant, conversation.KindEvent, "", raw)
	if err != nil {
		e.logger.Warn().Err(err).Str("conv", convID).Msg("工具过程事件落库失败")
		return
	}
	payload, err := json.Marshal(msg)
	if err != nil {
		return
	}
	if err := e.pub.Publish(ctx, convID, payload); err != nil {
		e.logger.Warn().Err(err).Str("conv", convID).Msg("工具过程事件广播失败（重连补历史可见）")
	}
}

// toolTimelineInterceptor 从 Deps 装配工具过程事件拦截器（依赖缺位时整体直通）。
func (d *Deps) toolTimelineInterceptor(conversationID string) registry.Interceptor {
	var w timelineWriter
	if d.Conversations != nil {
		w = d.Conversations
	}
	var p timelinePublisher
	if d.EventPublisher != nil {
		p = d.EventPublisher
	}
	return newToolTimelineEvents(w, p, d.Logger).interceptor(conversationID)
}

// truncateEventRunes 按 rune 截断并追加省略标记（上限 maxToolEventRunes）。
func truncateEventRunes(s string) string {
	runes := []rune(s)
	if len(runes) <= maxToolEventRunes {
		return s
	}
	return string(runes[:maxToolEventRunes]) + "…（截断）"
}
