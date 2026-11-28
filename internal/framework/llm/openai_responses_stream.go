package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/anthropics/anthropic-sdk-go/packages/ssestream"
)

// StreamChat 流式生成：正文 / 思考增量 + 聚合完成的工具调用 + done（带用量、结束原因与
// 待回传的推理项）。建流请求的错误在建流时就返回；输出上限被上游嫌大时降一半重建。
func (g *openAIResponsesGen) StreamChat(ctx context.Context, msgs []Message, tools []ToolSchema, maxTokens int) (<-chan StreamEvent, error) {
	req, err := g.request(msgs, tools, nil)
	if err != nil {
		return nil, fmt.Errorf("OpenAI Responses: 组装请求失败: %w", err)
	}
	req.Stream = true
	resp, err := g.post(ctx, req, maxTokens) //nolint:bodyclose // 响应体交给 pump 协程：读完或 ctx 结束后在那里关闭
	if err != nil {
		return nil, err
	}
	ch := make(chan StreamEvent, 32)
	go g.pump(ctx, resp, ch)
	return ch, nil
}

// responsesEvent 是流上的一帧：按 type 分派，各事件只用到其中一两个字段。
type responsesEvent struct {
	Type     string          `json:"type"`
	Delta    string          `json:"delta"`
	Item     json.RawMessage `json:"item"`
	Response json.RawMessage `json:"response"`
	Message  string          `json:"message"`
	Code     string          `json:"code"`
}

// pump 读完一条事件流并发出全部事件。事件名在 data 的 type 字段里（SSE 的 event 行只是它的
// 重复）；收尾事件（completed / incomplete / failed）带着完整的响应对象——用量、结束原因与
// 推理项从那里取。流在收尾事件之前断掉是 ErrStreamTruncated，部分正文不得当成完整响应。
func (g *openAIResponsesGen) pump(ctx context.Context, resp *http.Response, ch chan StreamEvent) {
	defer close(ch)
	emit := func(e StreamEvent) bool {
		select {
		case ch <- e:
			return true
		case <-ctx.Done():
			return false
		}
	}
	fail := func(err error) {
		emit(StreamEvent{Kind: StreamError, Err: err})
	}
	finish := func(raw json.RawMessage) {
		r, err := parseResponse(raw)
		if err != nil {
			// failed 状态的响应对象按上游失败交还；其余按不符合协议
			fail(err)
			return
		}
		res, _ := r.result(g.provider, g.model)
		var reasoning json.RawMessage
		if res.Thinking != nil {
			reasoning = res.Thinking.ReasoningItem
		}
		emit(StreamEvent{Kind: StreamDone, Usage: &res.Usage, FinishReason: r.finishReason(), ReasoningItem: reasoning})
	}

	dec := ssestream.NewDecoder(resp)
	if dec == nil {
		fail(fmt.Errorf("OpenAI Responses: 响应没有正文: %w", ErrUnexpectedResponse))
		return
	}
	defer func() { _ = resp.Body.Close() }()
	var sawEvents bool
	for dec.Next() {
		sawEvents = true
		var ev responsesEvent
		if err := json.Unmarshal(dec.Event().Data, &ev); err != nil {
			// 不是 JSON 的一帧：记不下来就跳过——收尾事件才是判定依据
			continue
		}
		switch ev.Type {
		case "response.output_text.delta", "response.refusal.delta":
			if !emit(StreamEvent{Kind: StreamText, Content: ev.Delta}) {
				return
			}
		case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
			if !emit(StreamEvent{Kind: StreamThinking, Content: ev.Delta}) {
				return
			}
		case "response.output_item.done":
			var it responsesItem
			if json.Unmarshal(ev.Item, &it) != nil {
				continue
			}
			if it.Type == responsesItemCall {
				args := json.RawMessage(it.Arguments)
				if len(args) == 0 {
					args = json.RawMessage("{}")
				}
				if !emit(StreamEvent{Kind: StreamToolCall, Tool: &ToolCall{ID: it.CallID, Name: it.Name, Arguments: args}}) {
					return
				}
			}
		case "response.completed", "response.incomplete":
			finish(ev.Response)
			return
		case "response.failed":
			var r responsesResponse
			if json.Unmarshal(ev.Response, &r) == nil && r.Status == "failed" {
				fail(r.failure())
			} else {
				fail(fmt.Errorf("OpenAI Responses: 上游生成失败: %s (%s)", ev.Message, ev.Code))
			}
			return
		case "error":
			fail(fmt.Errorf("OpenAI Responses: 流上错误（%s）: %s", ev.Code, ev.Message))
			return
		}
		if ctx.Err() != nil {
			return
		}
	}
	if ctx.Err() != nil {
		return // 消费方放弃了这条流：CollectStream 自己按 ctx 的错误收尾
	}
	if !sawEvents {
		// 一帧都不是事件流：2xx 响应体不是协议的流（网关把错误页包成了 200）
		fail(fmt.Errorf("OpenAI Responses: 2xx 响应体不是事件流: %w", ErrUnexpectedResponse))
		return
	}
	fail(ErrStreamTruncated)
}
