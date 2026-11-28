package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	openai "github.com/sashabaranov/go-openai"
)

// sseServer 以 SSE 应答任何请求，正文原样给出——协议适配器的流式路径走真实的 SDK 解码。
func sseServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func userMessage() []Message {
	return []Message{{Role: RoleUser, Content: "hi"}}
}

// Anthropic 流在 message_delta 里给出 stop_reason：输出被上限截断时是 max_tokens，此前聚合
// 时被写死成 stop，截断的调用在审计与「模型调用」页上都看不出来。
func TestAnthropicStreamCarriesFinishReason(t *testing.T) {
	const body = `event: message_start
data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"m","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":10,"output_tokens":1}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"{\"a\":"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"max_tokens","stop_sequence":null},"usage":{"output_tokens":4096}}

event: message_stop
data: {"type":"message_stop"}
`
	srv := sseServer(t, body)
	client := anthropic.NewClient(option.WithAPIKey("test"), option.WithBaseURL(srv.URL))
	gen, err := NewAnthropic(context.Background(), "p", AnthropicConfig{Model: "m", MaxTokens: 4096}, &client)
	if err != nil {
		t.Fatal(err)
	}
	ch, err := gen.(StreamChatGenerator).StreamChat(context.Background(), userMessage(), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := CollectStream(context.Background(), ch, nil, nil)
	if err != nil {
		t.Fatalf("流应正常收尾: %v", err)
	}
	if resp.Content != `{"a":` || resp.Usage.OutTokens != 4096 {
		t.Fatalf("正文与用量应聚合完整: %+v", resp)
	}
	if resp.FinishReason != "max_tokens" {
		t.Fatalf("结束原因应是协议给出的 max_tokens，实际 %q", resp.FinishReason)
	}
	if !HitOutputLimit(resp.FinishReason) {
		t.Fatal("max_tokens 结束的输出是被上限截断的")
	}
}

// OpenAI 兼容流的结束原因在最后一个带 finish_reason 的 chunk 里：length 同样是截断，且
// 该 chunk 之后流就结束了（待发的工具调用照常发出）。
func TestOpenAICompatStreamCarriesFinishReason(t *testing.T) {
	const body = `data: {"id":"c1","object":"chat.completion.chunk","model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":"{\"a\":"},"finish_reason":null}]}

data: {"id":"c1","object":"chat.completion.chunk","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"length"}]}

data: [DONE]

`
	srv := sseServer(t, body)
	cfg := openai.DefaultConfig("test")
	cfg.BaseURL = srv.URL + "/v1"
	client := openai.NewClientWithConfig(cfg)
	gen, err := NewOpenAICompat(context.Background(), "p", OpenAICompatConfig{Model: "m", MaxTokens: 4096}, client)
	if err != nil {
		t.Fatal(err)
	}
	ch, err := gen.(StreamChatGenerator).StreamChat(context.Background(), userMessage(), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := CollectStream(context.Background(), ch, nil, nil)
	if err != nil {
		t.Fatalf("流应正常收尾: %v", err)
	}
	if resp.Content != `{"a":` {
		t.Fatalf("正文应聚合完整: %+v", resp)
	}
	if resp.FinishReason != "length" {
		t.Fatalf("结束原因应是协议给出的 length，实际 %q", resp.FinishReason)
	}
	if !HitOutputLimit(resp.FinishReason) {
		t.Fatal("length 结束的输出是被上限截断的")
	}
}

// 收尾事件不带结束原因（适配器取不到）：仍按 stop 聚合——能走到收尾的流没有被截断或出错。
func TestCollectStreamDoneWithoutFinishReasonIsStop(t *testing.T) {
	resp, err := CollectStream(context.Background(), feed(StreamEvent{Kind: StreamDone}), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.FinishReason != "stop" || HitOutputLimit(resp.FinishReason) {
		t.Fatalf("没带结束原因的收尾应按 stop，实际 %q", resp.FinishReason)
	}
}

// HitOutputLimit 只认两种协议对截断的叫法；正常结束的取值都不是。
func TestHitOutputLimit(t *testing.T) {
	for reason, want := range map[string]bool{
		"max_tokens": true,
		"length":     true,
		"end_turn":   false,
		"stop":       false,
		"tool_use":   false,
		"tool_calls": false,
		"":           false,
	} {
		if got := HitOutputLimit(reason); got != want {
			t.Errorf("HitOutputLimit(%q) = %v，应为 %v", reason, got, want)
		}
	}
}

// 流式调用的审计行记协议给出的结束原因：此前成功收尾一律写 stop，「模型调用」页的「截断」
// 徽章对智能体的调用（全走流式）永远不会出现。
func TestInstrumentProvider_StreamRecordsFinishReason(t *testing.T) {
	sink := &captureSink{}
	p := InstrumentProvider(&streamStubProvider{events: []StreamEvent{
		{Kind: StreamText, Content: `{"a":`},
		{Kind: StreamDone, Usage: &Usage{OutTokens: 4096}, FinishReason: "max_tokens"},
	}}, sink)
	ch, err := p.Stream(context.Background(), Request{Messages: userMessage()})
	if err != nil {
		t.Fatal(err)
	}
	for range ch { //nolint:revive // 排空通道（审计在关闭后落库）
	}
	if len(sink.recs) != 1 {
		t.Fatalf("应落 1 行审计, got %d", len(sink.recs))
	}
	rec := sink.recs[0]
	if rec.FinishReason != "max_tokens" {
		t.Errorf("finish_reason 列应记 max_tokens，实际 %q", rec.FinishReason)
	}
	var got Response
	if err := json.Unmarshal(rec.Result, &got); err != nil {
		t.Fatalf("Result 应为 Response JSON: %v", err)
	}
	if got.FinishReason != "max_tokens" {
		t.Errorf("Result 里的 finish_reason 应为 max_tokens，实际 %q", got.FinishReason)
	}
	if rec.Err != "" {
		t.Errorf("被截断不是失败，error_message 应为空，实际 %q", rec.Err)
	}
}
