package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	openai "github.com/sashabaranov/go-openai"
)

// pickyUpstream 模拟一个输出上限只有 32768 的上游：请求的上限超过它，就以 400 点名上限字段
// 拒绝；否则正常应答。sent 记下每次请求的上限。
type pickyUpstream struct {
	mu   sync.Mutex
	sent []int
}

func (u *pickyUpstream) reject(limit int, msg string) (int, bool) {
	u.mu.Lock()
	u.sent = append(u.sent, limit)
	u.mu.Unlock()
	if limit > 32768 {
		return http.StatusBadRequest, true
	}
	return http.StatusOK, false
}

func (u *pickyUpstream) limits() []int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]int(nil), u.sent...)
}

const tooLarge = "max_tokens must be at most 32768 for this model"

func TestOutputLimitRejected(t *testing.T) {
	cases := map[string]struct {
		err  error
		want bool
	}{
		"400 点名 max_tokens":            {&HTTPError{Code: 400, Inner: errors.New("max_tokens must be at most 32768")}, true},
		"400 点名 max_completion_tokens": {&HTTPError{Code: 400, Inner: errors.New("Invalid 'max_completion_tokens'")}, true},
		"400 点名 max_output_tokens":     {&HTTPError{Code: 400, Inner: errors.New("max_output_tokens too large")}, true},
		"400 但与上限字段无关":                 {&HTTPError{Code: 400, Inner: errors.New("messages: first message must be user")}, false},
		"429 里提到 max_tokens 也不是拒绝上限":   {&HTTPError{Code: 429, Inner: errors.New("rate limit, max_tokens ignored")}, false},
		"不是 HTTP 错误":                   {errors.New("dial tcp: connection refused"), false},
	}
	for name, tc := range cases {
		if got := OutputLimitRejected(tc.err); got != tc.want {
			t.Errorf("%s: OutputLimitRejected = %v，应为 %v", name, got, tc.want)
		}
	}
}

// 上游嫌输出上限过大而拒绝：降一半重发，直到被接受；并把「这个模型只接受这么大」记进
// ClientPool——同一账户同一模型此后直接从记住的值起，别的模型不受影响。
func TestChatCompletionsLowersOutputLimitWhenRejected(t *testing.T) {
	up := &pickyUpstream{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			MaxTokens           int `json:"max_tokens"`
			MaxCompletionTokens int `json:"max_completion_tokens"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		limit := body.MaxTokens
		if limit == 0 {
			limit = body.MaxCompletionTokens
		}
		if status, reject := up.reject(limit, tooLarge); reject {
			http.Error(w, `{"error":{"message":"`+tooLarge+`","type":"invalid_request_error"}}`, status)
			return
		}
		_, _ = w.Write([]byte(`{"id":"c","object":"chat.completion","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()
	cfg := openai.DefaultConfig("test")
	cfg.BaseURL = srv.URL
	client := openai.NewClientWithConfig(cfg)
	pool := NewClientPool()
	build := func(model string) Generator {
		t.Helper()
		gen, err := NewOpenAICompat(context.Background(), "p", OpenAICompatConfig{
			Model: model, Limit: pool.OutputLimitFor(srv.URL, "test", model, 131072),
		}, client)
		if err != nil {
			t.Fatal(err)
		}
		return gen
	}

	if _, err := build("m").Generate(context.Background(), userMessage(), nil); err != nil {
		t.Fatalf("降下来之后应被接受: %v", err)
	}
	if got := up.limits(); len(got) != 3 || got[0] != 131072 || got[1] != 65536 || got[2] != 32768 {
		t.Fatalf("应 131072 → 65536 → 32768 逐次减半，实际 %v", got)
	}
	// 记住了：同一账户同一模型的新实例直接从 32768 起
	if _, err := build("m").Generate(context.Background(), userMessage(), nil); err != nil {
		t.Fatal(err)
	}
	if got := up.limits(); got[3] != 32768 {
		t.Fatalf("记住的上限应让新实例直接请求 32768，实际 %v", got)
	}
	// 别的模型不受影响：仍从配置的值起
	if _, err := build("other").Generate(context.Background(), userMessage(), nil); err != nil {
		t.Fatal(err)
	}
	if got := up.limits(); got[4] != 131072 {
		t.Fatalf("别的模型应从配置的上限起，实际 %v", got)
	}
}

func TestAnthropicLowersOutputLimitWhenRejected(t *testing.T) {
	const ok = anthropicSSE
	up := &pickyUpstream{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			MaxTokens int `json:"max_tokens"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if status, reject := up.reject(body.MaxTokens, tooLarge); reject {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"max_tokens: 131072 > 32768, which is the maximum allowed number of output tokens"}}`))
			return
		}
		_, _ = w.Write([]byte(ok))
	}))
	defer srv.Close()
	client := anthropic.NewClient(option.WithAPIKey("test"), option.WithBaseURL(srv.URL))
	pool := NewClientPool()
	gen, err := NewAnthropic(context.Background(), "p", AnthropicConfig{
		Model: "m", Limit: pool.OutputLimitFor(srv.URL, "test", "m", 131072),
	}, &client)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gen.Generate(context.Background(), userMessage(), nil); err != nil {
		t.Fatalf("降下来之后应被接受: %v", err)
	}
	if got := up.limits(); len(got) != 3 || got[2] != 32768 {
		t.Fatalf("应逐次减半到 32768，实际 %v", got)
	}
}

// 已经降到下限仍被拒绝：不再降，错误原样交还（它不是「上限过大」能解决的了）。
func TestOutputLimitGivesUpAtFloor(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":{"message":"`+tooLarge+`","type":"invalid_request_error"}}`, http.StatusBadRequest)
	}))
	defer srv.Close()
	cfg := openai.DefaultConfig("test")
	cfg.BaseURL = srv.URL
	pool := NewClientPool()
	gen, err := NewOpenAICompat(context.Background(), "p", OpenAICompatConfig{
		Model: "m", Limit: pool.OutputLimitFor(srv.URL, "test", "m", 4096),
	}, openai.NewClientWithConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}
	_, err = gen.Generate(context.Background(), userMessage(), nil)
	var he *HTTPError
	if !errors.As(err, &he) || he.Code != http.StatusBadRequest {
		t.Fatalf("应原样交还上游的 400，实际 %v", err)
	}
}
