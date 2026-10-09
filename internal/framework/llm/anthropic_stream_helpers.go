package llm

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/ssestream"
)

// prepareAnthropicRequest 准备 Anthropic 请求参数
func prepareAnthropicRequest(msgs []Message, tools []ToolSchema, model anthropic.Model, maxTokens int64) (anthropic.MessageNewParams, error) {
	systemBlocks, anthropicMsgs, err := toAnthropicMessages(msgs)
	if err != nil {
		return anthropic.MessageNewParams{}, fmt.Errorf("convert messages: %w", err)
	}
	
	anthropicTools, err := toAnthropicTools(tools)
	if err != nil {
		return anthropic.MessageNewParams{}, fmt.Errorf("convert tools: %w", err)
	}

	params := anthropic.MessageNewParams{
		Model:     model,
		MaxTokens: maxTokens,
		Messages:  anthropicMsgs,
	}
	
	if len(systemBlocks) > 0 {
		params.System = systemBlocks
	}
	if len(anthropicTools) > 0 {
		params.Tools = anthropicTools
	}

	return params, nil
}

// processAnthropicStream 处理 Anthropic 流式响应
func processAnthropicStream(ctx context.Context, stream *ssestream.Stream[anthropic.MessageStreamEventUnion], ch chan StreamEvent) {
	defer close(ch)
	defer func() {
		_ = stream.Close()
	}()

	var acc anthropic.Message
	toolArgs := map[int64][]byte{}

	emit := func(e StreamEvent) bool {
		select {
		case ch <- e:
			return true
		case <-ctx.Done():
			return false
		}
	}

	for stream.Next() {
		event := stream.Current()
		_ = acc.Accumulate(event)

		if !handleStreamEvent(event, &acc, toolArgs, emit) {
			return
		}
	}

	if err := stream.Err(); err != nil {
		emit(StreamEvent{Kind: StreamError, Err: wrapAnthropicErr(err)})
		return
	}

	u := Usage{
		InTokens:     int(acc.Usage.InputTokens),
		OutTokens:    int(acc.Usage.OutputTokens),
		CachedTokens: int(acc.Usage.CacheReadInputTokens),
	}
	emit(StreamEvent{Kind: StreamDone, Usage: &u})
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
