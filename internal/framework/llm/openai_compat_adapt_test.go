package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fieldGate 模拟只接受部分字段的上游：请求里带了被拒字段就回 400 点名该字段。
type fieldGate struct {
	mu       sync.Mutex
	rejects  map[string]string // 字段 → 400 错误文本
	requests []map[string]any
}

func (g *fieldGate) handler(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	g.mu.Lock()
	g.requests = append(g.requests, body)
	g.mu.Unlock()
	for field, msg := range g.rejects {
		if _, ok := body[field]; ok {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":` + jsonString(msg) + `,"type":"invalid_request_error"}}`))
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(openAIOK))
}

func jsonString(s string) string { b, _ := json.Marshal(s); return string(b) }

func compatGen(t *testing.T, g *fieldGate, limit OutputLimitParam) Generator {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(g.handler))
	t.Cleanup(srv.Close)
	gen, err := BuildGeneratorWithKey(context.Background(), ProviderSpec{
		Code: "p", Type: ProviderTypeOpenAICompat, BaseURL: srv.URL, Model: "m", APIKey: "k",
		MaxTokens: 64, OutputLimitParam: limit,
	}, NewClientPool())
	if err != nil {
		t.Fatal(err)
	}
	return gen
}

// 上游拒绝 max_tokens（推理模型的典型 400）：改发 max_completion_tokens 重试一次，
// 结论保留——后续请求直接用新字段，不再先撞一次 400。
func TestRequestCompat_SwitchesOutputLimitParamAndRemembers(t *testing.T) {
	g := &fieldGate{rejects: map[string]string{
		"max_tokens": "Unsupported parameter: 'max_tokens' is not supported with this model. Use 'max_completion_tokens' instead.",
	}}
	gen := compatGen(t, g, OutputLimitMaxTokens)
	ctx := context.Background()
	msgs := []Message{{Role: RoleUser, Content: "ping"}}

	if _, err := gen.Generate(ctx, msgs, nil); err != nil {
		t.Fatalf("应自适应成功: %v", err)
	}
	if _, err := gen.Generate(ctx, msgs, nil); err != nil {
		t.Fatalf("第二次: %v", err)
	}
	if len(g.requests) != 3 {
		t.Fatalf("首次 1 拒 + 1 重发，第二次直接成功，共 3 次请求；实际 %d", len(g.requests))
	}
	last := g.requests[2]
	if _, has := last["max_tokens"]; has || last["max_completion_tokens"] != float64(64) {
		t.Fatalf("结论应保留为 max_completion_tokens: %v", last)
	}
	if gen.(*openAICompatGen).OutputLimitParam() != OutputLimitMaxCompletionTokens {
		t.Fatal("生成器应报告自适应后的字段")
	}
}

// 反向：上游不认 max_completion_tokens（记录值过时），回退 max_tokens。
func TestRequestCompat_FallsBackToMaxTokens(t *testing.T) {
	g := &fieldGate{rejects: map[string]string{
		"max_completion_tokens": "Unrecognized request argument supplied: max_completion_tokens",
	}}
	gen := compatGen(t, g, OutputLimitMaxCompletionTokens)
	if _, err := gen.Generate(context.Background(), []Message{{Role: RoleUser, Content: "ping"}}, nil); err != nil {
		t.Fatalf("应回退成功: %v", err)
	}
	if _, has := g.requests[len(g.requests)-1]["max_tokens"]; !has {
		t.Fatal("应改发 max_tokens")
	}
}

// 上游不接受自定义温度：省略 temperature 重发。
func TestRequestCompat_OmitsRejectedTemperature(t *testing.T) {
	g := &fieldGate{rejects: map[string]string{
		"temperature": "Unsupported value: 'temperature' does not support 0.3 with this model. Only the default (1) value is supported.",
	}}
	gen := compatGen(t, g, OutputLimitMaxTokens).(*openAICompatGen)
	temp := 0.3
	if _, err := gen.GenerateWithOptions(context.Background(), []Message{{Role: RoleUser, Content: "ping"}}, nil, GenerateOptions{Temperature: &temp}); err != nil {
		t.Fatalf("应省略温度后成功: %v", err)
	}
	if _, has := g.requests[len(g.requests)-1]["temperature"]; has {
		t.Fatal("重发请求不应再带 temperature")
	}
}

// 与字段无关的 400（如消息格式错误）不触发改写重发，原样交给上层。
func TestRequestCompat_IgnoresUnrelatedBadRequest(t *testing.T) {
	g := &fieldGate{rejects: map[string]string{"model": "Invalid model name"}}
	gen := compatGen(t, g, OutputLimitMaxTokens)
	_, err := gen.Generate(context.Background(), []Message{{Role: RoleUser, Content: "ping"}}, nil)
	if err == nil || !strings.Contains(err.Error(), "Invalid model name") {
		t.Fatalf("应原样返回上游错误，got %v", err)
	}
	if len(g.requests) != 1 {
		t.Fatalf("无关 400 不应重发，实际 %d 次", len(g.requests))
	}
}

// 两个并发请求都带着旧字段被拒：先学到新结论的请求不应让另一个直接失败——是否重发只看
// 本请求发出的字段是否正是被拒的那个，与谁先更新共享结论无关。
func TestRequestCompat_ConcurrentRejectionsBothRecover(t *testing.T) {
	const msg = "Unsupported parameter: 'max_tokens' is not supported with this model. Use 'max_completion_tokens' instead."
	var arrived sync.WaitGroup
	arrived.Add(2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		if _, ok := body["max_tokens"]; ok {
			arrived.Done()
			arrived.Wait() // 两个请求都以旧字段到达后才一起拒绝
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":` + jsonString(msg) + `,"type":"invalid_request_error"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(openAIOK))
	}))
	t.Cleanup(srv.Close)
	gen, err := BuildGeneratorWithKey(context.Background(), ProviderSpec{
		Code: "p", Type: ProviderTypeOpenAICompat, BaseURL: srv.URL, Model: "m", APIKey: "k", MaxTokens: 64,
	}, NewClientPool())
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = gen.Generate(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("请求 %d 应按新结论重发成功，得 %v", i, err)
		}
	}
}
