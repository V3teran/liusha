package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// responsesOK 是一条完整的应答对象：正文、一个函数调用、一个带加密内容的推理项。
const responsesOK = `{"id":"resp_1","object":"response","status":"completed","output":[
 {"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"enc-1"},
 {"type":"message","id":"msg_1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"你好","annotations":[]}]},
 {"type":"function_call","id":"fc_1","call_id":"call_1","name":"probe","arguments":"{\"a\":1}","status":"completed"}
],"usage":{"input_tokens":10,"input_tokens_details":{"cached_tokens":4},"output_tokens":7,"output_tokens_details":{"reasoning_tokens":3},"total_tokens":17}}`

func responsesGen(t *testing.T, srv *httptest.Server, effort ReasoningEffort, vision bool) Generator {
	t.Helper()
	pool := NewClientPool()
	client, err := pool.GetOrCreateHTTP(srv.URL, "test", "")
	if err != nil {
		t.Fatal(err)
	}
	gen, err := NewOpenAIResponses(context.Background(), "p", OpenAIResponsesConfig{
		BaseURL: srv.URL, Model: "m", ReasoningEffort: effort, SupportsVision: vision,
		Limit: pool.OutputLimitFor(srv.URL, "test", "m", 4096),
	}, client)
	if err != nil {
		t.Fatal(err)
	}
	return gen
}

// captureJSON 记下上游收到的请求体，并按给定的正文与状态码应答。
func captureJSON(t *testing.T, status int, body string) (*httptest.Server, func() map[string]any) {
	t.Helper()
	var raw []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, func() map[string]any {
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		return m
	}
}

// 请求按协议的形状发出：输入项逐条对应内部消息（推理项原样放回它之后的调用项之前、
// 没等到结果的调用补回执），工具平铺，不留存、带上加密推理内容，思考深度经 reasoning.effort。
func TestResponses_RequestShape(t *testing.T) {
	srv, body := captureJSON(t, 200, responsesOK)
	gen := responsesGen(t, srv, ReasoningEffortMax, true)
	msgs := []Message{
		{Role: RoleSystem, Content: "章程"},
		{Role: RoleUser, ContentParts: []ContentPart{{Type: "text", Text: "看图"}, {Type: "image_url", ImageURL: &ImageContent{MediaType: "image/png", Base64Data: "AAAA"}}}},
		{Role: RoleAssistant, Thinking: &ThinkingBlock{ReasoningItem: json.RawMessage(`{"type":"reasoning","id":"rs_0","summary":[],"encrypted_content":"enc-0"}`)},
			Content: "先探一下", ToolCalls: []ToolCall{{ID: "call_a", Name: "probe", Arguments: json.RawMessage(`{"a":1}`)}, {ID: "call_b", Name: "probe", Arguments: json.RawMessage(`{}`)}}},
		{Role: RoleTool, ToolCallID: "call_a", Content: "结果"},
		{Role: RoleUser, Content: "继续"},
	}
	tools := []ToolSchema{{Name: "probe", Description: "探测", Parameters: json.RawMessage(`{"type":"object","properties":{}}`)}}
	temperature := 0.3
	if _, err := gen.(OptionsGenerator).GenerateWithOptions(context.Background(), msgs, tools, GenerateOptions{Temperature: &temperature}); err != nil {
		t.Fatal(err)
	}
	got := body()
	if got["model"] != "m" || got["store"] != false || got["stream"] != nil {
		t.Errorf("顶层字段不对: model=%v store=%v stream=%v", got["model"], got["store"], got["stream"])
	}
	if got["max_output_tokens"] != float64(4096) || got["temperature"] != 0.3 {
		t.Errorf("输出上限与温度不对: %v %v", got["max_output_tokens"], got["temperature"])
	}
	if fmt.Sprint(got["include"]) != "[reasoning.encrypted_content]" {
		t.Errorf("应带上加密推理内容: %v", got["include"])
	}
	if reasoning, _ := got["reasoning"].(map[string]any); reasoning["effort"] != "max" {
		t.Errorf("思考深度应经 reasoning.effort 下发: %v", got["reasoning"])
	}
	tool := got["tools"].([]any)[0].(map[string]any)
	if tool["type"] != "function" || tool["name"] != "probe" || tool["strict"] != false || tool["parameters"] == nil {
		t.Errorf("工具应平铺为 type/name/description/parameters: %v", tool)
	}
	items := got["input"].([]any)
	want := []string{"message:system", "message:user", "reasoning", "message:assistant", "function_call:call_a", "function_call:call_b", "function_call_output:call_b", "function_call_output:call_a", "message:user"}
	if len(items) != len(want) {
		t.Fatalf("输入项应为 %d 个，实际 %d: %v", len(want), len(items), items)
	}
	for i, it := range items {
		m := it.(map[string]any)
		desc := m["type"].(string)
		if role, ok := m["role"].(string); ok {
			desc += ":" + role
		}
		if id, ok := m["call_id"].(string); ok {
			desc += ":" + id
		}
		if desc != want[i] {
			t.Errorf("输入项 %d 为 %s，应为 %s", i, desc, want[i])
		}
	}
	if raw := items[2].(map[string]any); raw["encrypted_content"] != "enc-0" {
		t.Errorf("推理项应原样放回: %v", raw)
	}
	user := items[1].(map[string]any)
	parts := user["content"].([]any)
	if len(parts) != 2 || parts[0].(map[string]any)["type"] != "input_text" || parts[1].(map[string]any)["image_url"] != "data:image/png;base64,AAAA" {
		t.Errorf("带图的用户消息应是内容块数组: %v", user)
	}
	if out := items[7].(map[string]any); out["output"] != "结果" {
		t.Errorf("工具结果应原样: %v", out)
	}
	if out := items[6].(map[string]any); !strings.Contains(out["output"].(string), "未执行") {
		t.Errorf("没等到结果的调用应紧跟一条回执: %v", out)
	}
}

// 没设置思考深度就不发 reasoning；不支持视觉时图片换成占位文本。
func TestResponses_OmittedWhenUnset(t *testing.T) {
	srv, body := captureJSON(t, 200, responsesOK)
	gen := responsesGen(t, srv, "", false)
	_, err := gen.Generate(context.Background(), []Message{{Role: RoleUser, ContentParts: []ContentPart{{Type: "image_url", ImageURL: &ImageContent{MediaType: "image/png", Base64Data: "AAAA"}}}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := body()
	if _, has := got["reasoning"]; has {
		t.Errorf("没设置思考深度时不应下发 reasoning: %v", got["reasoning"])
	}
	if _, has := got["tools"]; has {
		t.Errorf("没有工具时不应下发 tools")
	}
	content := got["input"].([]any)[0].(map[string]any)["content"]
	if s, ok := content.(string); !ok || !strings.Contains(s, "[Image removed") {
		t.Errorf("不支持视觉时图片应换成占位文本: %v", content)
	}
}

// 应答折成结果：正文（含拒绝说明）、函数调用（ID 取 call_id）、第一个带加密内容的推理项原文、
// 用量与结束原因；下一轮请求把推理项放回。
func TestResponses_ResultAndReasoningPassThrough(t *testing.T) {
	srv, body := captureJSON(t, 200, responsesOK)
	gen := responsesGen(t, srv, "", false)
	res, err := gen.Generate(context.Background(), []Message{{Role: RoleUser, Content: "ping"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Content != "你好" || len(res.ToolCalls) != 1 || res.ToolCalls[0].ID != "call_1" || res.ToolCalls[0].Name != "probe" || string(res.ToolCalls[0].Arguments) != `{"a":1}` {
		t.Errorf("正文与工具调用不对: %+v %+v", res.Content, res.ToolCalls)
	}
	if res.Thinking == nil || !strings.Contains(string(res.Thinking.ReasoningItem), `"encrypted_content":"enc-1"`) {
		t.Errorf("推理项原文应留在思考块上: %+v", res.Thinking)
	}
	if res.Usage != (Usage{InTokens: 10, OutTokens: 7, CachedTokens: 4}) {
		t.Errorf("用量不对: %+v", res.Usage)
	}
	if res.FinishReason != "completed" {
		t.Errorf("结束原因应为 completed，实际 %q", res.FinishReason)
	}
	// 下一轮：带推理项的 assistant 消息放回输入
	_, err = gen.Generate(context.Background(), []Message{
		{Role: RoleUser, Content: "ping"},
		{Role: RoleAssistant, Content: res.Content, ToolCalls: res.ToolCalls, Thinking: res.Thinking},
		{Role: RoleTool, ToolCallID: "call_1", Content: "ok"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	items := body()["input"].([]any)
	if len(items) != 5 || items[1].(map[string]any)["type"] != "reasoning" || items[2].(map[string]any)["role"] != "assistant" || items[3].(map[string]any)["type"] != "function_call" || items[4].(map[string]any)["call_id"] != "call_1" {
		t.Errorf("下一轮的输入项应为 user → reasoning → assistant → function_call → function_call_output: %v", items)
	}
}

// 结束原因原样透出：输出达到上限的未完成响应是 max_output_tokens（调用方据此识别截断）；
// 上游生成失败的响应是错误；2xx 但不是响应对象判为不符合协议。
func TestResponses_FinishReasonsAndFailures(t *testing.T) {
	incomplete := strings.Replace(responsesOK, `"status":"completed","output":[`, `"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[`, 1)
	srv, _ := captureJSON(t, 200, incomplete)
	res, err := responsesGen(t, srv, "", false).Generate(context.Background(), []Message{{Role: RoleUser, Content: "ping"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.FinishReason != "max_output_tokens" || !HitOutputLimit(res.FinishReason) {
		t.Errorf("结束原因应为 max_output_tokens，实际 %q", res.FinishReason)
	}

	failed := `{"id":"resp_2","status":"failed","error":{"code":"server_error","message":"boom"},"output":[]}`
	srv, _ = captureJSON(t, 200, failed)
	if _, err := responsesGen(t, srv, "", false).Generate(context.Background(), []Message{{Role: RoleUser, Content: "ping"}}, nil); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("生成失败应带上上游的说明，实际 %v", err)
	}

	for name, body := range map[string]string{
		"网关错误页": `{"code":500,"msg":"404 NOT_FOUND","success":false}`,
		"HTML":  `<html>502</html>`,
	} {
		srv, _ := captureJSON(t, 200, body)
		if _, err := responsesGen(t, srv, "", false).Generate(context.Background(), []Message{{Role: RoleUser, Content: "ping"}}, nil); !errors.Is(err, ErrUnexpectedResponse) {
			t.Errorf("%s：应判为不符合协议，实际 %v", name, err)
		}
	}
}

// 非 2xx 归一为 HTTPError，响应体原文留给探测取说明；限流带上恢复时刻。
func TestResponses_HTTPErrors(t *testing.T) {
	srv, _ := captureJSON(t, 400, `{"error":{"message":"bad request","type":"invalid_request_error"}}`)
	_, err := responsesGen(t, srv, "", false).Generate(context.Background(), []Message{{Role: RoleUser, Content: "ping"}}, nil)
	var he *HTTPError
	var ue *UpstreamError
	if !errors.As(err, &he) || he.Code != 400 || !errors.As(err, &ue) || !strings.Contains(string(ue.Body), "bad request") {
		t.Fatalf("应为带上游错误体的 400，实际 %v", err)
	}

	srv429 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"slow down"}}`))
	}))
	defer srv429.Close()
	_, err = responsesGen(t, srv429, "", false).Generate(context.Background(), []Message{{Role: RoleUser, Content: "ping"}}, nil)
	if d, ok := RetryAfter(err, time.Now()); !ok || d <= 0 || !errors.As(err, &he) || he.Code != 429 {
		t.Fatalf("限流应带上恢复时刻，实际 %v", err)
	}
}

// 上游嫌输出上限过大：降一半重发（与另两种协议同一机制）。
func TestResponsesLowersOutputLimitWhenRejected(t *testing.T) {
	up := &pickyUpstream{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			MaxOutputTokens int `json:"max_output_tokens"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if status, reject := up.reject(body.MaxOutputTokens, tooLarge); reject {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":{"message":"` + tooLarge + `","type":"invalid_request_error"}}`))
			return
		}
		_, _ = w.Write([]byte(responsesOK))
	}))
	defer srv.Close()
	pool := NewClientPool()
	client, _ := pool.GetOrCreateHTTP(srv.URL, "test", "")
	gen, err := NewOpenAIResponses(context.Background(), "p", OpenAIResponsesConfig{
		BaseURL: srv.URL, Model: "m", Limit: pool.OutputLimitFor(srv.URL, "test", "m", 131072),
	}, client)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gen.Generate(context.Background(), userMessage(), nil); err != nil {
		t.Fatalf("降下来之后应被接受: %v", err)
	}
	if got := up.limits(); len(got) != 3 || got[0] != 131072 || got[2] != 32768 {
		t.Fatalf("应 131072 → 65536 → 32768 逐次减半，实际 %v", got)
	}
}

// 思考深度经 reasoning.effort 下发，没设置就不发。
func TestResponses_ReasoningEffort(t *testing.T) {
	for effort, want := range map[ReasoningEffort]any{ReasoningEffortMedium: "medium", ReasoningEffortMax: "max", "": nil} {
		srv, body := captureJSON(t, 200, responsesOK)
		if _, err := responsesGen(t, srv, effort, false).Generate(context.Background(), userMessage(), nil); err != nil {
			t.Fatal(err)
		}
		got := body()["reasoning"]
		if effort == "" {
			if got != nil {
				t.Errorf("没设置思考深度时不应下发 reasoning: %v", got)
			}
			continue
		}
		if fmt.Sprint(got) != fmt.Sprintf("map[effort:%s]", want) {
			t.Errorf("思考深度 %q：reasoning = %v，应为 effort=%s", effort, got, want)
		}
	}
}

// sseServer 以事件流应答，记下请求体；每个事件两行（event 与 data）。
func responsesStreamServer(t *testing.T, events ...string) (*httptest.Server, func() map[string]any) {
	t.Helper()
	var raw []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		for _, e := range events {
			typ := e[:strings.IndexByte(e, ' ')]
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", typ, e[strings.IndexByte(e, ' ')+1:])
		}
	}))
	t.Cleanup(srv.Close)
	return srv, func() map[string]any {
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		return m
	}
}

func delta(typ, text string) string {
	return fmt.Sprintf(`%s {"type":"%s","delta":%q}`, typ, typ, text)
}

func itemDone(typ, item string) string {
	return fmt.Sprintf(`%s {"type":"%s","item":%s}`, typ, typ, item)
}

// 流式：正文与思考增量逐段发出，工具调用在项完成时发出，收尾事件带着用量、结束原因与推理项。
func TestResponsesStream_AggregatesLikeComplete(t *testing.T) {
	srv, body := responsesStreamServer(t,
		`response.created {"type":"response.created","response":{"id":"resp_1","status":"in_progress"}}`,
		delta("response.reasoning_summary_text.delta", "想一想"),
		delta("response.output_text.delta", "你"),
		delta("response.output_text.delta", "好"),
		itemDone("response.output_item.done", `{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"enc-1"}`),
		itemDone("response.output_item.done", `{"type":"function_call","id":"fc_1","call_id":"call_1","name":"probe","arguments":"{}","status":"completed"}`),
		`response.completed {"type":"response.completed","sequence_number":9,"response":`+strings.ReplaceAll(responsesOK, "\n", " ")+"}",
	)
	gen := responsesGen(t, srv, ReasoningEffortHigh, false)
	ch, err := gen.(StreamChatGenerator).StreamChat(context.Background(), []Message{{Role: RoleUser, Content: "ping"}}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := CollectStream(context.Background(), ch, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "你好" {
		t.Errorf("正文应聚合增量: %q", resp.Content)
	}
	if resp.Thinking == nil || resp.Thinking.Text != "想一想" || !strings.Contains(string(resp.Thinking.ReasoningItem), "enc-1") {
		t.Errorf("思考增量与推理项原文都应带到: %+v", resp.Thinking)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].ID != "call_1" || resp.ToolCalls[0].Name != "probe" {
		t.Errorf("工具调用不对: %+v", resp.ToolCalls)
	}
	if resp.Usage.OutTokens != 7 || resp.FinishReason != "completed" {
		t.Errorf("用量与结束原因应取自收尾事件: %+v %q", resp.Usage, resp.FinishReason)
	}
	if got := body(); got["stream"] != true {
		t.Errorf("流式请求应带 stream=true: %v", got["stream"])
	}
}

// 流上的收尾与异常：未完成响应的结束原因、生成失败、流上错误、收尾之前断流、2xx 但不是事件流。
func TestResponsesStream_Terminations(t *testing.T) {
	// 事件体必须在一行上：应答对象里的换行压成空格
	incomplete := strings.ReplaceAll(strings.Replace(responsesOK, `"status":"completed"`, `"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}`, 1), "\n", " ")
	cases := map[string]struct {
		events []string
		reason string
		err    string // 期望的错误片段；空 = 应成功收尾
	}{
		"未完成：输出达到上限": {[]string{`response.incomplete {"type":"response.incomplete","response":` + incomplete + "}"}, "max_output_tokens", ""},
		"生成失败":       {[]string{`response.failed {"type":"response.failed","response":{"id":"r","status":"failed","error":{"code":"server_error","message":"boom"},"output":[]}}`}, "", "boom"},
		"流上错误":       {[]string{`error {"type":"error","code":"server_error","message":"overloaded"}`}, "", "overloaded"},
		"收尾之前断流":     {[]string{delta("response.output_text.delta", "半截")}, "", "stream closed before terminal event"},
	}
	for name, tc := range cases {
		srv, _ := responsesStreamServer(t, tc.events...)
		ch, err := responsesGen(t, srv, "", false).(StreamChatGenerator).StreamChat(context.Background(), userMessage(), nil, 0)
		if err != nil {
			t.Fatalf("%s: 建流: %v", name, err)
		}
		resp, err := CollectStream(context.Background(), ch, nil, nil)
		if tc.err != "" {
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Errorf("%s：应报错含 %q，实际 %v", name, tc.err, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if resp.FinishReason != tc.reason {
			t.Errorf("%s：结束原因应为 %q，实际 %q", name, tc.reason, resp.FinishReason)
		}
	}
	// 2xx 但不是事件流（网关把错误页包成 200）
	srv, _ := captureJSON(t, 200, `{"code":500,"msg":"404 NOT_FOUND","success":false}`)
	ch, err := responsesGen(t, srv, "", false).(StreamChatGenerator).StreamChat(context.Background(), userMessage(), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CollectStream(context.Background(), ch, nil, nil); !errors.Is(err, ErrUnexpectedResponse) {
		t.Errorf("不是事件流的 2xx 响应应判为不符合协议，实际 %v", err)
	}
}

// 消费方在途中放弃：按 ctx 的错误收尾，不记成断流。
func TestResponsesStream_AbandonedByCaller(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"开\"}\n\n")
		w.(http.Flusher).Flush()
		<-release
	}))
	defer srv.Close()
	defer close(release)
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := responsesGen(t, srv, "", false).(StreamChatGenerator).StreamChat(ctx, userMessage(), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	<-ch
	cancel()
	if _, err := CollectStream(ctx, ch, nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("消费方放弃应按取消收尾，实际 %v", err)
	}
}
