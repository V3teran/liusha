package main

import (
	"context"
	"encoding/json"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/conversation"
	"github.com/V3teran/liusha/internal/einoagent"
	"github.com/V3teran/liusha/internal/scanstream"
)

// event_sink.go：einoagent.EventSink 的 scanner 实现（阶段B2b）。
//
// 每个 agent 过程事件（tool_call / tool_result，含 exploitation 内部）：
//  1. 落 conversation message（PG，得 seq，可回看 + 断线重连补历史）
//  2. publish message JSON 到 redis（实时推 api SSE handler → 前端）
//
// **异步写穿（write-behind）**：OnScanEvent 只把事件投进有界 channel（不阻塞 agent ReAct 热路径），
// 单 writer goroutine 串行落库 + publish。原同步实现每事件卡在 agent 路径上做 DB 往返，
// active 几百步即几百次同步往返——本改把 DB 写挪出热路径。
//   - 单 writer 串行 → seq 单调 + publish 有序（顺带消除并行子代理并发写的乱序）。
//   - channel 有界：满则 OnScanEvent 阻塞（背压；仅持续过载时发生，突发由 buffer 吸收）。
//   - Close() 在 agent run 结束后调：关 channel + 等 drain 写完剩余事件（用 Background ctx，
//     run ctx 取消也能 flush 落库，保证前端重连能补到全部历史）。
//
// 仅当对话发起（ConversationID 非空）时装配；asynq 自动入口不装（sink nil，不发事件）。

// eventQueueSize 是进程内过程事件队列容量。突发由 buffer 吸收，持续过载才回压 agent。
const eventQueueSize = 256

// einoEventSink 把 agent 过程事件异步落库 + 实时广播。用 *einoEventSink（持 goroutine 状态）。
type einoEventSink struct {
	conversations  *conversation.Store
	publisher      *scanstream.Publisher
	conversationID string
	logger         zerolog.Logger
	ch             chan einoagent.ScanEvent
	done           chan struct{}
	// writeFn 是单 writer 对每条事件的处理动作；生产为 persist，单测注入 fake 验证队列契约。
	writeFn func(einoagent.ScanEvent)
}

// newEinoEventSink 构造 sink 并启动 writer goroutine。
func newEinoEventSink(convs *conversation.Store, pub *scanstream.Publisher, convID string, logger zerolog.Logger) *einoEventSink {
	s := &einoEventSink{
		conversations:  convs,
		publisher:      pub,
		conversationID: convID,
		logger:         logger,
		ch:             make(chan einoagent.ScanEvent, eventQueueSize),
		done:           make(chan struct{}),
	}
	s.writeFn = s.persist
	go s.run()
	return s
}

// run 是单 writer：串行消费 channel → writeFn。channel 关闭后写完剩余再退出（close(done)）。
func (s *einoEventSink) run() {
	defer close(s.done)
	for ev := range s.ch {
		s.writeFn(ev)
	}
}

// OnScanEvent 把事件投进队列（满则阻塞=背压）；run ctx 取消则丢弃（扫描已中止，丢在途事件可接受）。
func (s *einoEventSink) OnScanEvent(ctx context.Context, ev einoagent.ScanEvent) {
	select {
	case s.ch <- ev:
	case <-ctx.Done():
		s.logger.Warn().Str("conv", s.conversationID).Str("tool", ev.ToolName).
			Msg("run 取消，丢弃在途过程事件")
	}
}

// Close 关队列并等 writer 写完所有缓冲事件（agent run 结束后调，保证落库完整）。
func (s *einoEventSink) Close() {
	close(s.ch)
	<-s.done
}

// persist 落 message + publish。用 Background ctx——run ctx 可能已取消，但缓冲事件仍须落库
// （前端重连靠 PG 补历史）。best-effort：任一步失败仅记日志。
func (s *einoEventSink) persist(ev einoagent.ScanEvent) {
	ctx := context.Background()
	role, content := describeEvent(ev)
	metadata, _ := json.Marshal(ev) // ScanEvent 无不可序列化字段，err 必 nil

	msg, err := s.conversations.AppendMessage(ctx, s.conversationID, role, conversation.KindEvent, content, metadata)
	if err != nil {
		s.logger.Warn().Err(err).Str("conv", s.conversationID).Str("tool", ev.ToolName).
			Msg("过程事件落 message 失败（不阻塞扫描）")
		return
	}

	payload, err := json.Marshal(msg)
	if err != nil {
		return
	}
	if err := s.publisher.Publish(ctx, s.conversationID, payload); err != nil {
		s.logger.Warn().Err(err).Str("conv", s.conversationID).
			Msg("过程事件 publish redis 失败（已落 PG，重连补历史可见）")
	}
}

// describeEvent 把 ScanEvent 转成 message 的 role + content。
// 完整结构（ToolName/Args/Result/DurationMs/Err）在 metadata，前端可精细渲染；
// content 是可读摘要（回看列表 / 无 metadata 解析时的兜底）。
func describeEvent(ev einoagent.ScanEvent) (conversation.Role, string) {
	switch ev.Kind {
	case einoagent.ScanEventToolCall:
		// agent 决定调工具 → assistant 视角。
		return conversation.RoleAssistant, "调用工具 " + ev.ToolName
	case einoagent.ScanEventToolResult:
		if ev.Err != "" {
			return conversation.RoleTool, ev.ToolName + " 出错: " + ev.Err
		}
		return conversation.RoleTool, ev.Result
	default:
		return conversation.RoleSystem, ev.ToolName
	}
}
