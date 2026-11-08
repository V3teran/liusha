package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

// 非流式路径必须透传请求级 MaxTokens（与流式同口径）：否则裁决的 token 上限、
// 强制收尾的预算调整在非流式调用上全部失效。
func TestComplete_PassesRequestMaxTokens(t *testing.T) {
	var gotMax int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			MaxTokens int `json:"max_tokens"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotMax = body.MaxTokens
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()

	cfg := openai.DefaultConfig("k")
	cfg.BaseURL = srv.URL
	gen, err := NewOpenAICompat(context.Background(), "p", OpenAICompatConfig{Model: "m", MaxTokens: 8000}, openai.NewClientWithConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewProvider(gen).Complete(context.Background(), Request{Messages: []Message{{Role: RoleUser, Content: "hi"}}, MaxTokens: 1500}); err != nil {
		t.Fatal(err)
	}
	if gotMax != 1500 {
		t.Fatalf("请求级 MaxTokens 应覆盖默认值，上游收到 %d", gotMax)
	}
}
