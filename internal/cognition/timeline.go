// timeline.go 工具过程事件：每次工具调用/结果落会话时间线并实时广播——
// 对话框可见「agent 正在对目标做什么」（调用 → 观测），与思考流（在想什么）
// 互补构成完整过程面。错误随 tool_result 内联（err 字段），失败一眼可见。

package cognition

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/conversation"
	"github.com/V3teran/liusha/internal/reasoning"
	"github.com/V3teran/liusha/internal/registry"
)

// 时间线事件的单字段窗口（args 与 result 各自适用，按字符计）：时间线要的是可读过程，不是全文。
// 超过时留开头与结尾、中间写明省略了多少字符——结尾必须留：命令结果的退出码、完整输出的
// 文件路径都在末尾（此前只取开头，一份长输出的末尾连同 exit code 一起被切掉，还看不出被切过）。
// 完整内容另有去处：命令输出超过工具结果窗口时由沙盒落盘到任务工作区，模型看到的那一份留在
// llm_invocation 的消息历史里。
const (
	maxToolEventRunes  = 4000 // 没超过就原样
	toolEventHeadRunes = 3000
	toolEventTailRunes = maxToolEventRunes - toolEventHeadRunes
)

// timelineWriter 时间线事件的落库通道（*conversation.Store 满足）。
type timelineWriter interface {
	AppendMessage(ctx context.Context, conversationID string, role conversation.Role, kind conversation.Kind, content string, metadata json.RawMessage) (conversation.Message, error)
}

// timelinePublisher 时间线事件的 SSE 广播通道（*convstream.Publisher 满足）。
type timelinePublisher interface {
	PublishMessage(ctx context.Context, conversationID string, msg any) error
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
		agent := AgentFromContext(ctx)
		// call_id 贯穿本次调用的两个事件：并发执行同名工具时（多 action 并行）
		// result 完成序与 call 发起序可能交错，仅凭 (agent, tool) 的 FIFO 配对
		// 会把 result 配到 A 的卡上。事件内联身份后消费端按 ID 精确配对。
		callID := uuid.NewString()
		// round_id 把事件归到 agent 的当前一轮（与那一轮的终稿同一个标识）：同一 agent
		// 并行多轮时，时间线只凭先后顺序分不清事件属于哪一轮。
		roundID := reasoning.RoundID(ctx)
		e.emit(ctx, conversationID, withRoundID(map[string]any{
			"kind":       "tool_call",
			"call_id":    callID,
			"agent_name": agent,
			"tool_name":  t.Name(),
			"args":       windowEventRunes(string(args)),
		}, roundID))

		start := time.Now()
		res, err := next(ctx, t, args)

		errMsg := ""
		if res.Error != "" {
			errMsg = res.Error
		} else if err != nil {
			errMsg = err.Error()
		}
		e.emit(ctx, conversationID, withRoundID(map[string]any{
			"kind":        "tool_result",
			"call_id":     callID,
			"agent_name":  agent,
			"tool_name":   t.Name(),
			"result":      windowEventRunes(res.Output),
			"duration_ms": time.Since(start).Milliseconds(),
			"err":         windowEventRunes(errMsg),
		}, roundID))
		return res, err
	}
}

// withRoundID 在 roundID 非空时写进事件：不在任何一轮里的调用（没有 BeginRound 的
// 调用方）不写这个键——时间线上它按 agent 与先后顺序归组。
func withRoundID(event map[string]any, roundID string) map[string]any {
	if roundID != "" {
		event["round_id"] = roundID
	}
	return event
}

// emit 落一条 agent 过程事件并广播 SSE（best-effort：失败仅告警，工具执行本体
// 不受影响——时间线是观测面，不构成执行依赖）。
func (e *toolTimelineEvents) emit(ctx context.Context, conversationID string, meta map[string]any) {
	raw, err := json.Marshal(meta)
	if err != nil {
		e.logger.Warn().Err(err).Msg("工具过程事件序列化失败")
		return
	}
	msg, err := e.writer.AppendMessage(ctx, conversationID,
		conversation.RoleAssistant, conversation.KindEvent, "", raw)
	if err != nil {
		e.logger.Warn().Err(err).Str("conversation_id", conversationID).Msg("工具过程事件落库失败")
		return
	}
	if err := e.pub.PublishMessage(ctx, conversationID, msg); err != nil {
		e.logger.Warn().Err(err).Str("conversation_id", conversationID).Msg("工具过程事件广播失败（重连补历史可见）")
	}
}

// Emit 对外发射一条过程事件（发现卡等非工具事件与工具事件共用同一出口、
// 同一 best-effort 语义）。conversationID 为空或依赖缺位时 no-op。
func (e *toolTimelineEvents) Emit(ctx context.Context, conversationID string, meta map[string]any) {
	if conversationID == "" || e.writer == nil || e.pub == nil {
		return
	}
	e.emit(ctx, conversationID, meta)
}

// toolTimelineInterceptor 从 Deps 装配工具过程事件拦截器（依赖缺位时整体直通）。
func (d *Deps) toolTimelineInterceptor(conversationID string) registry.Interceptor {
	return d.toolTimelineEvents().interceptor(conversationID)
}

// toolTimelineEvents 从 Deps 装配时间线事件出口（依赖缺位时各发射点 no-op）。
func (d *Deps) toolTimelineEvents() *toolTimelineEvents {
	var w timelineWriter
	if d.Conversations != nil {
		w = d.Conversations
	}
	var p timelinePublisher
	if d.EventPublisher != nil {
		p = d.EventPublisher
	}
	return newToolTimelineEvents(w, p, d.Logger)
}

// windowEventRunes 把时间线事件的一个字段收进 maxToolEventRunes 个字符：没超过原样；超过时开头
// 留 toolEventHeadRunes 个字符、结尾留 toolEventTailRunes 个字符，中间写明省略了多少字符。
func windowEventRunes(s string) string {
	runes := []rune(s)
	if len(runes) <= maxToolEventRunes {
		return s
	}
	omitted := len(runes) - toolEventHeadRunes - toolEventTailRunes
	return string(runes[:toolEventHeadRunes]) + fmt.Sprintf("…（中间省略 %d 字）…", omitted) + string(runes[len(runes)-toolEventTailRunes:])
}
