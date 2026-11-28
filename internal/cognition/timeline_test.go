package cognition

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/conversation"
	"github.com/V3teran/liusha/internal/reasoning"
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
	return conversation.Message{Seq: 1, Metadata: meta}, nil
}

// fakeTimelinePub 记录 SSE 广播。
type fakeTimelinePub struct {
	mu       sync.Mutex
	payloads int
}

func (f *fakeTimelinePub) PublishMessage(_ context.Context, _ string, _ any) error {
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

// deadlineAwareWriter 模拟真实的存储：ctx 已经结束（超时、取消）时写入立即失败。
type deadlineAwareWriter struct {
	fakeTimelineWriter
}

func (f *deadlineAwareWriter) AppendMessage(ctx context.Context, conversationID string, role conversation.Role, kind conversation.Kind, content string, meta json.RawMessage) (conversation.Message, error) {
	if err := ctx.Err(); err != nil {
		return conversation.Message{}, err
	}
	return f.fakeTimelineWriter.AppendMessage(ctx, conversationID, role, kind, content, meta)
}

// expireTool 模拟一次超时的工具调用：执行中它的 ctx 到期，随后带着结果返回。
type expireTool struct{ cancel context.CancelFunc }

func (t *expireTool) Name() string            { return "corpus_search" }
func (t *expireTool) ShortDesc() string       { return "检索" }
func (t *expireTool) Desc() string            { return "检索知识库" }
func (t *expireTool) Schema() json.RawMessage { return json.RawMessage(`{}`) }
func (t *expireTool) Timeout() time.Duration  { return time.Second }
func (t *expireTool) Execute(ctx context.Context, _ json.RawMessage) (registry.ToolResult, error) {
	t.cancel()
	<-ctx.Done()
	return registry.ToolResult{Error: "检索超时"}, nil
}

// 工具调用超时之后，它的结果事件照样落库并广播：时间线是观测面，自己的写入有自己的时限，不随
// 被观测调用的截止时间作废。此前发事件用的是工具调用的 ctx——凡是超时的调用（实测：向量化接口
// 超时时的 corpus_search），它的结果事件必然丢失，而超时恰恰是最该留下过程的一类。
func TestToolTimelineInterceptor_ResultEventOutlivesToolDeadline(t *testing.T) {
	w, pub := &deadlineAwareWriter{}, &fakeTimelinePub{}
	e := newToolTimelineEvents(w, pub, zerolog.Nop())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tool := &expireTool{cancel: cancel}

	_, err := e.interceptor("conv-1")(ctx, tool, json.RawMessage(`{}`), func(ctx context.Context, t registry.Tool, args []byte) (registry.ToolResult, error) {
		return t.Execute(ctx, args)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(w.metas) != 2 || pub.payloads != 2 {
		t.Fatalf("调用与结果两条事件都应落库并广播: metas=%d payloads=%d", len(w.metas), pub.payloads)
	}
	var last struct {
		Kind string `json:"kind"`
		Err  string `json:"err"`
	}
	if json.Unmarshal(w.metas[1], &last) != nil || last.Kind != "tool_result" || last.Err != "检索超时" {
		t.Fatalf("结果事件应带着错误落库: %s", w.metas[1])
	}
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

// 时间线事件字段过长时留开头与结尾、中间写明省略多少：结尾必须保住——命令结果的退出码与完整
// 输出的文件路径都在末尾。
func TestWindowEventRunes(t *testing.T) {
	if windowEventRunes("短文本") != "短文本" {
		t.Fatalf("短文本不应截断")
	}
	head := strings.Repeat("头", toolEventHeadRunes)
	middle := strings.Repeat("中", 500)
	tail := strings.Repeat("尾", toolEventTailRunes)
	got := windowEventRunes(head + middle + tail)
	want := head + fmt.Sprintf("…（中间省略 %d 字）…", len([]rune(middle))) + tail
	if got != want {
		t.Fatalf("应留开头 %d 字、结尾 %d 字并在中间写明省略 %d 字，实际开头 %q", toolEventHeadRunes, toolEventTailRunes, len([]rune(middle)), got[:24])
	}
	if n := len([]rune(got)); n > maxToolEventRunes+40 {
		t.Fatalf("总长 %d 超出窗口太多", n)
	}
}

// 工具过程事件带 ctx 所在轮的标识：与这一轮的终稿（reasoning 消息）同一个 round_id，
// 时间线据它把事件归到那一轮名下。不在轮里（没有 BeginRound 的调用方）不写这个键。
func TestToolTimelineInterceptor_CarriesRoundID(t *testing.T) {
	w := &fakeTimelineWriter{}
	interceptor := newToolTimelineEvents(w, &fakeTimelinePub{}, zerolog.Nop()).interceptor("conv-1")
	next := registry.ExecuteFunc(func(_ context.Context, _ registry.Tool, _ []byte) (registry.ToolResult, error) {
		return registry.ToolResult{Output: "观测原文"}, nil
	})

	ctx := reasoning.BeginRound(WithAgent(context.Background(), "executor"))
	if _, err := interceptor(ctx, echoTool{}, []byte(`{}`), next); err != nil {
		t.Fatal(err)
	}
	if _, err := interceptor(context.Background(), echoTool{}, []byte(`{}`), next); err != nil {
		t.Fatal(err)
	}

	want := reasoning.RoundID(ctx)
	if want == "" {
		t.Fatal("前置：ctx 应在一轮里")
	}
	for i, meta := range w.metas[:2] {
		var ev struct {
			RoundID string `json:"round_id"`
		}
		if err := json.Unmarshal(meta, &ev); err != nil {
			t.Fatalf("事件 %d 解析: %v", i, err)
		}
		if ev.RoundID != want {
			t.Fatalf("事件 %d 应带本轮标识 %q, got %q", i, want, ev.RoundID)
		}
	}
	var outside map[string]any
	if err := json.Unmarshal(w.metas[2], &outside); err != nil {
		t.Fatalf("事件解析: %v", err)
	}
	if _, ok := outside["round_id"]; ok {
		t.Fatalf("不在轮里的事件不应带标识, got %v", outside)
	}
}
