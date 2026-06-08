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
// 每个 agent 过程事件（tool_call / tool_result，含 striker 内部）：
//  1. 落 conversation message（PG，得 seq，可回看 + 断线重连补历史）
//  2. publish message JSON 到 redis（实时推 api SSE handler → 前端）
//
// 仅当对话发起（ConversationID 非空）时装配；asynq 自动入口不装（sink nil，不发事件）。

// einoEventSink 把 agent 过程事件落库 + 实时广播。
type einoEventSink struct {
	conversations  *conversation.Store
	publisher      *scanstream.Publisher
	conversationID string
	logger         zerolog.Logger
}

// OnScanEvent 落 message + publish。best-effort：任一步失败仅记日志，不影响扫描主流程。
func (s einoEventSink) OnScanEvent(ctx context.Context, ev einoagent.ScanEvent) {
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
