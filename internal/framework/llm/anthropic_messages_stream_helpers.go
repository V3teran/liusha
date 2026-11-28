package llm

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/ssestream"
)

// anthropicRequest 组装一次 messages 请求。maxTokens 是调用方按 provider 配置给定的输出上限——
// 协议必填；请求级预算（调用方按任务收窄，如 judge 1500）可能小于 provider 级 thinking budget
// （如 4096）——协议硬约束 budget < max_tokens（严格实现直接 400，宽容后端则思考吞光正文预算），
// 所以此处把 budget clamp 到 maxTokens 的 3/4：思考有上限、正文恒留 1/4 余量，两边都不违约。
// 开启思考时流式路径不设 temperature——该模式下协议只允许 1，不设即协议默认。
func anthropicRequest(systemBlocks []anthropic.TextBlockParam, msgs []anthropic.MessageParam, tools []anthropic.ToolUnionParam, model anthropic.Model, maxTokens, thinkingBudget int64) anthropic.MessageNewParams {
	params := anthropic.MessageNewParams{
		Model:     model,
		MaxTokens: maxTokens,
		Messages:  msgs,
	}
	if len(systemBlocks) > 0 {
		params.System = systemBlocks
	}
	if len(tools) > 0 {
		params.Tools = tools
	}
	if thinkingBudget > 0 {
		params.Thinking = anthropic.ThinkingConfigParamOfEnabled(clampThinkingBudget(thinkingBudget, maxTokens))
	}
	return params
}

// clampThinkingBudget 把思考预算钳到 maxTokens 的 3/4：协议硬约束 budget < max_tokens，
// 请求级上限收窄时（如判定 1500）思考有上限、正文恒留 1/4 余量。流式与非流式共用。
func clampThinkingBudget(thinkingBudget, maxTokens int64) int64 {
	if ceiling := maxTokens * 3 / 4; thinkingBudget > ceiling {
		return ceiling
	}
	return thinkingBudget
}

// processAnthropicStream 处理 Anthropic 流式响应
// processAnthropicStream 处理 Anthropic 流式响应：start 按给定的输出上限建流。建流的错误要到
// 首次读取才出现——那时还没有发出任何事件，所以上游嫌上限过大时可以降一半重建，消费方看不到。
// 已经发出过事件就不再重建：重来会让增量重复。
func processAnthropicStream(
	ctx context.Context,
	ch chan StreamEvent,
	lim *OutputLimit,
	limit int,
	start func(limit int) *ssestream.Stream[anthropic.MessageStreamEventUnion],
) {
	defer close(ch)
	emitted := false
	emit := func(e StreamEvent) bool {
		emitted = true
		select {
		case ch <- e:
			return true
		case <-ctx.Done():
			return false
		}
	}
	for {
		err := drainAnthropicStream(ctx, start(limit), emit)
		if err == nil {
			return
		}
		if next, ok := lim.rejected(wrapAnthropicErr(err), limit); ok && !emitted {
			limit = next
			continue
		}
		emit(StreamEvent{Kind: StreamError, Err: wrapAnthropicErr(err)})
		return
	}
}

// drainAnthropicStream 读完一条流并发出全部事件；返回流的错误（正常收尾为 nil）。
func drainAnthropicStream(ctx context.Context, stream *ssestream.Stream[anthropic.MessageStreamEventUnion], emit func(StreamEvent) bool) error {
	defer func() {
		_ = stream.Close()
	}()

	var acc anthropic.Message
	toolArgs := map[int64][]byte{}

	for stream.Next() {
		event := stream.Current()
		_ = acc.Accumulate(event)

		if !handleStreamEvent(event, &acc, toolArgs, emit) {
			return nil
		}
	}

	if err := stream.Err(); err != nil {
		return err
	}
	// 一个事件都没收到：2xx 响应体不是消息流（网关把错误页包成了 200）。消息流一定以
	// message_delta 给出结束原因，拿不到它就是没收到过消息。
	if acc.StopReason == "" {
		return fmt.Errorf("2xx 响应体不是消息流: %w", ErrUnexpectedResponse)
	}

	u := Usage{
		InTokens:     int(acc.Usage.InputTokens),
		OutTokens:    int(acc.Usage.OutputTokens),
		CachedTokens: int(acc.Usage.CacheReadInputTokens),
	}
	// 思考块签名在流收尾才完整（SDK Accumulate 聚合出完整 ThinkingBlock）；
	// 随 StreamDone 透出，与 CollectStream 聚合的思考文本组成 ThinkingBlock。
	sig := ""
	for _, block := range acc.Content {
		if tb, ok := block.AsAny().(anthropic.ThinkingBlock); ok && tb.Signature != "" {
			sig = tb.Signature
			break
		}
	}
	// 结束原因随收尾事件透出（message_delta 的 stop_reason）：输出被上限截断时是 max_tokens。
	emit(StreamEvent{Kind: StreamDone, Usage: &u, Signature: sig, FinishReason: string(acc.StopReason)})
	return nil
}

// handleStreamEvent 处理单个流事件
func handleStreamEvent(event anthropic.MessageStreamEventUnion, acc *anthropic.Message, toolArgs map[int64][]byte, emit func(StreamEvent) bool) bool {
	switch ev := event.AsAny().(type) {
	case anthropic.ContentBlockDeltaEvent:
		return handleContentBlockDelta(ev, toolArgs, emit)
	case anthropic.ContentBlockStartEvent:
		return handleContentBlockStart(ev, toolArgs)
	case anthropic.ContentBlockStopEvent:
		return handleContentBlockStop(ev, acc, toolArgs, emit)
	}
	return true
}

// handleContentBlockDelta 处理内容块增量事件
func handleContentBlockDelta(ev anthropic.ContentBlockDeltaEvent, toolArgs map[int64][]byte, emit func(StreamEvent) bool) bool {
	switch d := ev.Delta.AsAny().(type) {
	case anthropic.TextDelta:
		if d.Text != "" {
			if !emit(StreamEvent{Kind: StreamText, Content: d.Text}) {
				return false
			}
		}
	case anthropic.ThinkingDelta:
		if d.Thinking != "" {
			if !emit(StreamEvent{Kind: StreamThinking, Content: d.Thinking}) {
				return false
			}
		}
	case anthropic.InputJSONDelta:
		toolArgs[ev.Index] = append(toolArgs[ev.Index], []byte(d.PartialJSON)...)
	}
	return true
}

// handleContentBlockStart 处理内容块开始事件
func handleContentBlockStart(ev anthropic.ContentBlockStartEvent, toolArgs map[int64][]byte) bool {
	if _, ok := ev.ContentBlock.AsAny().(anthropic.ToolUseBlock); ok {
		toolArgs[ev.Index] = []byte{}
	}
	return true
}

// handleContentBlockStop 处理内容块停止事件
func handleContentBlockStop(ev anthropic.ContentBlockStopEvent, acc *anthropic.Message, toolArgs map[int64][]byte, emit func(StreamEvent) bool) bool {
	if int(ev.Index) < len(acc.Content) {
		if tu, ok := acc.Content[ev.Index].AsAny().(anthropic.ToolUseBlock); ok {
			args := toolArgs[ev.Index]
			if len(args) == 0 {
				args = []byte("{}")
			}
			tc := &ToolCall{ID: tu.ID, Name: tu.Name, Arguments: json.RawMessage(args)}
			delete(toolArgs, ev.Index)
			if !emit(StreamEvent{Kind: StreamToolCall, Tool: tc}) {
				return false
			}
		}
	}
	return true
}
