package llm

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/sashabaranov/go-openai"
)

// prepareOpenAICompatRequest 准备 OpenAI 兼容请求参数
func prepareOpenAICompatRequest(msgs []Message, tools []ToolSchema, model string, maxTokens, defaultMaxTokens int, supportsVision bool) (openai.ChatCompletionRequest, error) {
	openaiMsgs, err := toOpenAIMessages(msgs, supportsVision)
	if err != nil {
		return openai.ChatCompletionRequest{}, fmt.Errorf("convert messages: %w", err)
	}
	openaiTools, err := toOpenAITools(tools)
	if err != nil {
		return openai.ChatCompletionRequest{}, fmt.Errorf("convert tools: %w", err)
	}

	creq := openai.ChatCompletionRequest{
		Model:    model,
		Messages: openaiMsgs,
		Stream:   true,
	}
	if len(openaiTools) > 0 {
		creq.Tools = openaiTools
	}
	
	mt := defaultMaxTokens
	if maxTokens > 0 {
		mt = maxTokens
	}
	if mt > 0 {
		creq.MaxTokens = mt
	}

	return creq, nil
}

// processOpenAICompatStream 处理 OpenAI 兼容流式响应
func processOpenAICompatStream(ctx context.Context, stream *openai.ChatCompletionStream, ch chan StreamEvent) {
	defer close(ch)
	defer func() {
		_ = stream.Close()
	}()

	partial := map[int]*ToolCall{}

	emit := func(e StreamEvent) bool {
		select {
		case ch <- e:
			return true
		case <-ctx.Done():
			return false
		}
	}

	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			emit(StreamEvent{Kind: StreamError, Err: wrapOpenAICompatErr(err)})
			return
		}
		if len(chunk.Choices) == 0 {
			continue
		}

		if !handleOpenAICompatChunk(chunk, partial, emit) {
			return
		}

		if isOpenAICompatFinished(chunk) {
			if !emitPendingToolCalls(partial, emit) {
				return
			}
			break
		}
	}

	// OpenAI 流式 chunk 不返回 usage，发空 Usage
	emit(StreamEvent{Kind: StreamDone, Usage: &Usage{}})
}

// handleOpenAICompatChunk 处理单个 OpenAI 兼容 chunk
func handleOpenAICompatChunk(chunk openai.ChatCompletionStreamResponse, partial map[int]*ToolCall, emit func(StreamEvent) bool) bool {
	delta := chunk.Choices[0].Delta

	if delta.Content != "" {
		if !emit(StreamEvent{Kind: StreamText, Content: delta.Content}) {
			return false
		}
	}

	for _, tc := range delta.ToolCalls {
		if tc.Index == nil {
			continue
		}
		i := *tc.Index
		if _, ok := partial[i]; !ok {
			partial[i] = &ToolCall{ID: tc.ID, Name: tc.Function.Name, Arguments: []byte("")}
		} else {
			if tc.ID != "" {
				partial[i].ID = tc.ID
			}
			if tc.Function.Name != "" {
				partial[i].Name = tc.Function.Name
			}
		}
		partial[i].Arguments = append(partial[i].Arguments, []byte(tc.Function.Arguments)...)
	}

	return true
}

// isOpenAICompatFinished 检查是否已完成
func isOpenAICompatFinished(chunk openai.ChatCompletionStreamResponse) bool {
	return chunk.Choices[0].FinishReason == openai.FinishReasonToolCalls ||
		chunk.Choices[0].FinishReason == openai.FinishReasonStop
}

// emitPendingToolCalls 发出所有待处理的工具调用
func emitPendingToolCalls(partial map[int]*ToolCall, emit func(StreamEvent) bool) bool {
	for i := 0; i < len(partial); i++ {
		tc, ok := partial[i]
		if !ok {
			continue
		}
		if len(tc.Arguments) == 0 {
			tc.Arguments = []byte("{}")
		}
		if !emit(StreamEvent{Kind: StreamToolCall, Tool: tc}) {
			return false
		}
	}
	return true
}
