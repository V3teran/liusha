package cognition

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/conversation"
	"github.com/V3teran/liusha/internal/registry"
)

// fakeTimelineWriter 记录时间线落库调用（工具过程事件）。
type fakeTimelineWriter struct {
	mu    sync.Mutex
	metas []json.RawMessage
}

func (f *fakeTimelineWriter) AppendMessage(_ context.Context, _ string, _ conversation.Role, _ conversation.Kind, _ string, meta json.RawMessage) (conversation.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.metas = append(f.metas, meta)
	return conversation.Message{ID: "m", Metadata: meta}, nil
}

// fakeTimelinePub 记录 SSE 广播。
type fakeTimelinePub struct {
	mu       sync.Mutex
	payloads int
}

func (f *fakeTimelinePub) Publish(_ context.Context, _ string, _ []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.payloads++
	return nil
}

// echoTool 是最简工具替身。
type echoTool struct{}

func (echoTool) Name() string            { return "http_request" }
func (echoTool) ShortDesc() string       { return "探测" }
func (echoTool) Desc() string            { return "发 HTTP 请求" }
func (echoTool) Schema() json.RawMessage { return json.RawMessage(`{}`) }
func (echoTool) Timeout() time.Duration  { return time.Second }
func (echoTool) Execute(_ context.Context, args json.RawMessage) (registry.ToolResult, error) {
	return registry.ToolResult{Output: "观测原文：" + string(args)}, nil
}

// 一次工具调用产生两条事件：tool_call（执行前，携带 args）→ tool_result
// （执行后，携带 result/duration/err）；agent 归属取自 ctx，两条事件均广播 SSE。
func TestToolTimelineInterceptor_EmitsCallAndResult(t *testing.T) {
	w, pub := &fakeTimelineWriter{}, &fakeTimelinePub{}
	interceptor := newToolTimelineEvents(w, pub, zerolog.Nop()).interceptor("conv-1")

	next := registry.ExecuteFunc(func(_ context.Context, _ registry.Tool, _ []byte) (registry.ToolResult, error) {
		return registry.ToolResult{Output: "观测原文"}, nil
	})
	ctx := WithAgent(context.Background(), "executor")
	res, err := interceptor(ctx, echoTool{}, []byte(`{"url":"http://example.com"}`), next)
	if err != nil || res.Output != "观测原文" {
		t.Fatalf("拦截器应透传执行结果: res=%+v err=%v", res, err)
	}

	if len(w.metas) != 2 {
		t.Fatalf("应落 tool_call + tool_result 两条事件, got %d", len(w.metas))
	}
	var call struct {
		Kind      string `json:"kind"`
		AgentName string `json:"agent_name"`
		ToolName  string `json:"tool_name"`
		Args      string `json:"args"`
	}
	if err := json.Unmarshal(w.metas[0], &call); err != nil {
		t.Fatalf("tool_call 解析: %v", err)
	}
	if call.Kind != "tool_call" || call.AgentName != "executor" || call.ToolName != "http_request" {
		t.Fatalf("tool_call = %+v", call)
	}
	if call.Args != `{"url":"http://example.com"}` {
		t.Fatalf("args = %q", call.Args)
	}

	var result struct {
		Kind       string `json:"kind"`
		AgentName  string `json:"agent_name"`
		ToolName   string `json:"tool_name"`
		Result     string `json:"result"`
		DurationMs int64  `json:"duration_ms"`
		Err        string `json:"err"`
	}
	if err := json.Unmarshal(w.metas[1], &result); err != nil {
		t.Fatalf("tool_result 解析: %v", err)
	}
	if result.Kind != "tool_result" || result.AgentName != "executor" || result.ToolName != "http_request" {
		t.Fatalf("tool_result = %+v", result)
	}
	if result.Result != "观测原文" || result.Err != "" || result.DurationMs < 0 {
		t.Fatalf("tool_result 内容 = %+v", result)
	}
	if pub.payloads != 2 {
		t.Fatalf("两条事件都应广播 SSE, got %d", pub.payloads)
	}
}

// 工具失败：err 内联进 tool_result（时间线红卡数据源），错误不吞不改性。
func TestToolTimelineInterceptor_ErrorInlined(t *testing.T) {
	w, pub := &fakeTimelineWriter{}, &fakeTimelinePub{}
	interceptor := newToolTimelineEvents(w, pub, zerolog.Nop()).interceptor("conv-1")

	next := registry.ExecuteFunc(func(_ context.Context, _ registry.Tool, _ []byte) (registry.ToolResult, error) {
		return registry.ToolResult{Output: "", Error: "连接超时"}, nil
	})
	if _, err := interceptor(context.Background(), echoTool{}, []byte(`{}`), next); err != nil {
		t.Fatalf("错误经 ToolResult.Error 表达, err=%v", err)
	}

	var result struct {
		Err string `json:"err"`
	}
	if err := json.Unmarshal(w.metas[1], &result); err != nil {
		t.Fatalf("解析: %v", err)
	}
	if result.Err != "连接超时" {
		t.Fatalf("err 应内联 = %q", result.Err)
	}
}

// 无会话（后台扫描）或依赖缺位：直通零事件。
func TestToolTimelineInterceptor_PassThroughWithoutConversation(t *testing.T) {
	w, pub := &fakeTimelineWriter{}, &fakeTimelinePub{}
	next := registry.ExecuteFunc(func(_ context.Context, _ registry.Tool, _ []byte) (registry.ToolResult, error) {
		return registry.ToolResult{Output: "x"}, nil
	})

	// 空 conversationID：直通。
	ic := newToolTimelineEvents(w, pub, zerolog.Nop()).interceptor("")
	if _, err := ic(context.Background(), echoTool{}, []byte(`{}`), next); err != nil {
		t.Fatalf("直通失败: %v", err)
	}
	if len(w.metas) != 0 {
		t.Fatalf("无会话不应产生事件, got %d", len(w.metas))
	}

	// 依赖缺位（无落库通道）：同样直通。
	if _, err := newToolTimelineEvents(nil, pub, zerolog.Nop()).interceptor("conv-1")(context.Background(), echoTool{}, []byte(`{}`), next); err != nil {
		t.Fatalf("依赖缺位直通失败: %v", err)
	}
	if pub.payloads != 0 {
		t.Fatalf("依赖缺位不应广播, got %d", pub.payloads)
	}
}

// 超长输出按 rune 截断（时间线保可读，原文在遥测表）。
func TestTruncateEventRunes(t *testing.T) {
	long := string(make([]rune, maxToolEventRunes+10))
	got := truncateEventRunes(long)
	want := maxToolEventRunes + len([]rune("…（截断）"))
	if gotRunes := len([]rune(got)); gotRunes != want {
		t.Fatalf("截断长度 = %d, want %d", gotRunes, want)
	}
	if truncateEventRunes("短文本") != "短文本" {
		t.Fatalf("短文本不应截断")
	}
}
