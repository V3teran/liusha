package llm

import (
	"context"
	"fmt"
)

// CollectStream 消费流式事件通道并聚合成完整 Response；onDelta 在每个文本
// 增量到达时被调（可空——纯聚合场景）。通道以 StreamError 收尾时返回错误；
// ctx 取消时立即返回。FinishReason 恒为 "stop"（工具调用由 ToolCalls 表达）。
func CollectStream(ctx context.Context, ch <-chan StreamEvent, onDelta func(chunk string)) (Response, error) {
	var resp Response
	for ev := range ch {
		switch ev.Kind {
		case StreamText, StreamThinking:
			if ev.Kind == StreamText {
				resp.Content += ev.Content
				if onDelta != nil && ev.Content != "" {
					onDelta(ev.Content)
				}
			}
		case StreamToolCall:
			if ev.Tool != nil {
				resp.ToolCalls = append(resp.ToolCalls, *ev.Tool)
			}
		case StreamDone:
			if ev.Usage != nil {
				resp.Usage = *ev.Usage
			}
		case StreamError:
			if ev.Err != nil {
				return Response{}, fmt.Errorf("stream error: %w", ev.Err)
			}
			return Response{}, fmt.Errorf("stream closed with error event")
		}
		if ctx.Err() != nil {
			return Response{}, ctx.Err()
		}
	}
	resp.FinishReason = "stop"
	return resp, nil
}
