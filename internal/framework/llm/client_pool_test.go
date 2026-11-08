package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// ambient auth env（如调用方工具链自设的 ANTHROPIC_AUTH_TOKEN）不得泄漏进出站请求：
// SDK 默认会把它附加为 Authorization: Bearer <值>，兼容网关优先读该头会无视我们真正的
// x-api-key（实测 401「令牌已过期或验证不正确」）。本系统密钥统一走 x-api-key。
func TestGetOrCreateAnthropic_ScrubsAmbientAuthHeader(t *testing.T) {
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "PROXY_MANAGED")

	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","type":"message","role":"assistant","model":"m","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer srv.Close()

	pool := NewClientPool()
	cli, err := pool.GetOrCreateAnthropic(srv.URL, "real-key")
	if err != nil {
		t.Fatal(err)
	}
	c, _ := NewAnthropic(context.Background(), "p", AnthropicConfig{BaseURL: srv.URL, Model: "m", MaxTokens: 8}, cli)
	if _, err := c.Generate(context.Background(), []Message{{Role: RoleUser, Content: "ping"}}, nil); err != nil {
		t.Fatalf("generate: %v", err)
	}

	if got.Get("X-Api-Key") != "real-key" {
		t.Errorf("x-api-key 应携带真实密钥，得 %q", got.Get("X-Api-Key"))
	}
	if got.Get("Authorization") != "" {
		t.Errorf("Authorization 头应被擦除（环境变量不得泄漏），得 %q", got.Get("Authorization"))
	}
	out, _ := json.Marshal(got)
	_ = out
}
