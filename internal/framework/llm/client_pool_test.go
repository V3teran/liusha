package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

const anthropicOK = `{"id":"x","type":"message","role":"assistant","model":"m","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`
const openAIOK = `{"id":"x","object":"chat.completion","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`

// captureServer 记录最后一次请求的头，并按协议回一条合法响应。
func captureServer(t *testing.T, body string) (*httptest.Server, *http.Header) {
	t.Helper()
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

// 出站请求只携带 provider 记录的那一种凭据头：SDK 自带的、以及 SDK 从环境变量
// （ANTHROPIC_API_KEY / ANTHROPIC_AUTH_TOKEN）自动注入的外来值一律清掉——兼容网关
// 优先读 Authorization，外来占位值会让真实密钥被无视（实测 401）。
func TestClientPool_SendsExactlyTheRecordedCredential(t *testing.T) {
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "AMBIENT_TOKEN")
	t.Setenv("ANTHROPIC_API_KEY", "AMBIENT_KEY")
	ctx := context.Background()
	msgs := []Message{{Role: RoleUser, Content: "ping"}}

	cases := []struct {
		name       string
		protocol   string
		scheme     AuthScheme
		wantHeader string
		wantValue  string
		absent     []string
	}{
		{"anthropic 标准", ProviderTypeAnthropic, "", "X-Api-Key", "real-key", []string{"Authorization"}},
		{"anthropic Bearer", ProviderTypeAnthropic, AuthBearer, "Authorization", "Bearer real-key", []string{"X-Api-Key"}},
		{"openai 标准", ProviderTypeOpenAICompat, "", "Authorization", "Bearer real-key", []string{"X-Api-Key"}},
		{"openai x-api-key", ProviderTypeOpenAICompat, AuthXAPIKey, "X-Api-Key", "real-key", []string{"Authorization"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := openAIOK
			if tc.protocol == ProviderTypeAnthropic {
				body = anthropicOK
			}
			srv, got := captureServer(t, body)
			gen, err := BuildGeneratorWithKey(ctx, ProviderSpec{
				Code: "p", Type: tc.protocol, BaseURL: srv.URL, Model: "m", APIKey: "real-key",
				AuthScheme: tc.scheme, MaxTokens: 8,
			}, NewClientPool())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := gen.Generate(ctx, msgs, nil); err != nil {
				t.Fatalf("generate: %v", err)
			}
			if v := got.Get(tc.wantHeader); v != tc.wantValue {
				t.Fatalf("%s 应为 %q，得 %q", tc.wantHeader, tc.wantValue, v)
			}
			for _, h := range tc.absent {
				if v := got.Get(h); v != "" {
					t.Fatalf("%s 不应出现，得 %q", h, v)
				}
			}
		})
	}
}

// 同一地址+密钥、不同鉴权方式是两个 client（复用键含鉴权方式）；空方式按协议标准归一。
func TestClientPool_KeysByAuthScheme(t *testing.T) {
	pool := NewClientPool()
	a, _ := pool.GetOrCreateOpenAI("https://x", "k", "")
	b, _ := pool.GetOrCreateOpenAI("https://x", "k", AuthBearer)
	c, _ := pool.GetOrCreateOpenAI("https://x", "k", AuthXAPIKey)
	if a != b {
		t.Fatal("空鉴权方式应归一为协议标准方式，复用同一 client")
	}
	if a == c {
		t.Fatal("不同鉴权方式应是不同 client")
	}
}

// Close 后池清空：再取同一配置得到新 client（生命周期有界的池用完即弃，不驻留密钥）。
func TestClientPool_CloseReleasesClients(t *testing.T) {
	pool := NewClientPool()
	before, _ := pool.GetOrCreateAnthropic("https://x", "k", "")
	pool.Close()
	after, _ := pool.GetOrCreateAnthropic("https://x", "k", "")
	if before == after {
		t.Fatal("Close 后应重新创建 client")
	}
}

// 凭据只发给配置的端点：跨源重定向（另一主机 / 端口 / 协议）必须被拒绝，否则 transport
// 会在跟随后的请求上重新写入 provider 密钥，把它交给重定向目标。
func TestCredentialedClient_RefusesCrossOriginRedirect(t *testing.T) {
	for _, scheme := range []AuthScheme{AuthBearer, AuthXAPIKey} {
		t.Run(string(scheme), func(t *testing.T) {
			var leaked http.Header
			target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				leaked = r.Header.Clone()
			}))
			t.Cleanup(target.Close)
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, target.URL+"/v1/chat", http.StatusTemporaryRedirect)
			}))
			t.Cleanup(origin.Close)

			resp, err := credentialedHTTPClient("SECRET", scheme).Post(origin.URL+"/v1/chat", "application/json", nil)
			if err == nil {
				_ = resp.Body.Close()
				t.Fatal("跨源重定向应被拒绝")
			}
			if leaked != nil {
				t.Fatalf("重定向目标不应收到任何请求，实际收到头: %v", leaked)
			}
		})
	}
}

// 同源重定向（路径变化）照常跟随并携带凭据。
func TestCredentialedClient_FollowsSameOriginRedirect(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/old" {
			http.Redirect(w, r, "/new", http.StatusTemporaryRedirect)
			return
		}
		got = r.Header.Get("Authorization")
	}))
	t.Cleanup(srv.Close)

	resp, err := credentialedHTTPClient("SECRET", AuthBearer).Post(srv.URL+"/old", "application/json", nil)
	if err != nil {
		t.Fatalf("同源重定向应跟随: %v", err)
	}
	_ = resp.Body.Close()
	if got != "Bearer SECRET" {
		t.Fatalf("同源跟随后的请求应携带凭据，得 %q", got)
	}
}
