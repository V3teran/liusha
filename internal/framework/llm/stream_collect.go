package llm

import (
	"context"
	"errors"
	"fmt"
)

// ErrStreamTruncated 表示流在收到收尾事件（StreamDone / StreamError）之前就关闭了。
// provider 在 ctx 取消时会放弃发送错误事件直接关通道——此时已收到的只是部分正文，
// 不得当作完整响应。
var ErrStreamTruncated = errors.New("stream closed before terminal event")

// CollectStream 消费流式事件通道并聚合成完整 Response。
//
// 双通道增量回调（均可空——纯聚合场景）：
//   - onText：正文文本增量（ReAct thought / 最终回答的逐字流）；
//   - onThinking：扩展思考增量（模型内部 CoT 的逐字流，anthropic thinking block）。
//
// 思考文本增量聚合为 Response.Thinking.Text；收尾事件的 Signature 补全
// ThinkingBlock（供多轮 tool_use 的规范回传）。通道以 StreamError 收尾、或未见
// 收尾事件即关闭（ErrStreamTruncated）时返回错误；ctx 取消时立即返回。成功时
// FinishReason 是协议给出的结束原因（见 finishReasonOf）。
func CollectStream(ctx context.Context, ch <-chan StreamEvent, onText func(chunk string), onThinking func(chunk string)) (Response, error) {
	var resp Response
	done := false
	for ev := range ch {
		switch ev.Kind {
		case StreamText:
			resp.Content += ev.Content
			if onText != nil && ev.Content != "" {
				onText(ev.Content)
			}
		case StreamThinking:
			if ev.Content != "" {
				if resp.Thinking == nil {
					resp.Thinking = &ThinkingBlock{}
				}
				resp.Thinking.Text += ev.Content
				if onThinking != nil {
					onThinking(ev.Content)
				}
			}
		case StreamToolCall:
			if ev.Tool != nil {
				resp.ToolCalls = append(resp.ToolCalls, *ev.Tool)
			}
		case StreamDone:
			done = true
			resp.FinishReason = finishReasonOf(ev)
			if ev.Usage != nil {
				resp.Usage = *ev.Usage
			}
			if resp.Thinking != nil {
				resp.Thinking.Signature = ev.Signature
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
	if err := ctx.Err(); err != nil {
		return Response{}, err
	}
	if !done {
		return Response{}, ErrStreamTruncated
	}
	return resp, nil
}

// finishReasonOf 是收尾事件给出的结束原因；适配器没给的按 stop——流在收尾之前断掉走不到这里
// （那是 ErrStreamTruncated），带着收尾事件结束的流即使没写明原因也是正常停下。
func finishReasonOf(ev StreamEvent) string {
	if ev.FinishReason != "" {
		return ev.FinishReason
	}
	return "stop"
}
