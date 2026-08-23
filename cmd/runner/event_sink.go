package main

import (
	"context"
	"encoding/json"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/conversation"
	"github.com/V3teran/liusha/internal/scanagent"
	"github.com/V3teran/liusha/internal/scanstream"
)

const eventQueueSize = 256

// eventSink 把 agent 过程事件异步落库 + 实时广播。
// 单 writer goroutine 串行消费 channel，保证 seq 单调 + publish 有序。
// Close() 在 agent run 结束后调：关 channel + 等 drain 写完。
type eventSink struct {
	conversations  *conversation.Store
	publisher      *scanstream.Publisher
	conversationID string
	logger         zerolog.Logger
	ch             chan scanagent.ScanEvent
	done           chan struct{}
	writeFn        func(scanagent.ScanEvent)
}

func newEventSink(convs *conversation.Store, pub *scanstream.Publisher, convID string, logger zerolog.Logger) *eventSink {
	s := &eventSink{
		conversations:  convs,
		publisher:      pub,
		conversationID: convID,
		logger:         logger,
		ch:             make(chan scanagent.ScanEvent, eventQueueSize),
		done:           make(chan struct{}),
	}
	s.writeFn = s.persist
	go s.run()
	return s
}

func (s *eventSink) run() {
	defer close(s.done)
	for ev := range s.ch {
		s.writeFn(ev)
	}
}

// OnScanEvent 投进队列；run ctx 取消则丢弃。
func (s *eventSink) OnScanEvent(ctx context.Context, ev scanagent.ScanEvent) {
	select {
	case s.ch <- ev:
	case <-ctx.Done():
		s.logger.Warn().Str("conv", s.conversationID).Str("tool", ev.ToolName).
			Msg("run 取消，丢弃在途过程事件")
	}
}

// Close 关队列并等 writer 写完所有缓冲事件。
func (s *eventSink) Close() {
	close(s.ch)
	<-s.done
}

type reasoningDeltaFrame struct {
	Delta     bool   `json:"delta"`
	Text      string `json:"text"`
	AgentName string `json:"agent_name,omitempty"`
}

func (s *eventSink) publishDelta(text, agentName string) {
	payload, err := json.Marshal(reasoningDeltaFrame{Delta: true, Text: text, AgentName: agentName})
	if err != nil {
		return
	}
	if err := s.publisher.Publish(context.Background(), s.conversationID, payload); err != nil {
		s.logger.Warn().Err(err).Str("conv", s.conversationID).Msg("流式推理增量 publish 失败（瞬时帧，丢弃可接受）")
	}
}

// persist 落 message + publish（用 Background ctx，run ctx 取消后仍需落库）。
func (s *eventSink) persist(ev scanagent.ScanEvent) {
	if ev.Kind == scanagent.ScanEventReasoningDelta {
		s.publishDelta(ev.Text, ev.AgentName)
		return
	}

	ctx := context.Background()
	role, content := describeEvent(ev)
	metadata, _ := json.Marshal(ev)

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

func describeEvent(ev scanagent.ScanEvent) (conversation.Role, string) {
	switch ev.Kind {
	case scanagent.ScanEventReasoning:
		return conversation.RoleAssistant, ev.Text
	case scanagent.ScanEventSpawn:
		return conversation.RoleAssistant, spawnSummary(ev.Args)
	case scanagent.ScanEventToolCall:
		return conversation.RoleAssistant, "调用工具 " + ev.ToolName
	case scanagent.ScanEventToolResult:
		if ev.Err != "" {
			return conversation.RoleTool, ev.ToolName + " 出错: " + ev.Err
		}
		return conversation.RoleTool, ev.Result
	default:
		return conversation.RoleSystem, ev.ToolName
	}
}

func spawnSummary(args string) string {
	var in struct {
		SubagentType string `json:"subagent_type"`
		Description  string `json:"description"`
	}
	if err := json.Unmarshal([]byte(args), &in); err != nil || in.SubagentType == "" {
		return "派发子代理"
	}
	if in.Description == "" {
		return "派发 " + in.SubagentType
	}
	return "派发 " + in.SubagentType + "：" + in.Description
}
