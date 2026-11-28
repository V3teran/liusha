package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	openai "github.com/sashabaranov/go-openai"
)

// captureBody 记下上游收到的请求体（JSON），并按给定的正文应答。
func captureBody(t *testing.T, body string) (*httptest.Server, func() map[string]any) {
	t.Helper()
	var raw []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, func() map[string]any {
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		return m
	}
}

// 思考深度：每种协议按自己的字段下发，取值是各家的通行叫法；没设置就不发这个参数。
func TestReasoningEffort_PerProtocol(t *testing.T) {
	ctx := context.Background()
	msgs := []Message{{Role: RoleUser, Content: "ping"}}

	t.Run("Chat Completions：reasoning_effort", func(t *testing.T) {
		for effort, want := range map[ReasoningEffort]any{ReasoningEffortLow: "low", ReasoningEffortMax: "max", "": nil} {
			srv, body := captureBody(t, openAIOK)
			cfg := openai.DefaultConfig("test")
			cfg.BaseURL = srv.URL
			gen, err := NewOpenAIChatCompletions(ctx, "p", OpenAIChatCompletionsConfig{
				Model: "m", ReasoningEffort: effort,
				Limit: NewClientPool().OutputLimitFor(srv.URL, "test", "m", 4096),
			}, openai.NewClientWithConfig(cfg))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := gen.Generate(ctx, msgs, nil); err != nil {
				t.Fatal(err)
			}
			if got := body()["reasoning_effort"]; got != want {
				t.Errorf("思考深度 %q：reasoning_effort = %v，应为 %v", effort, got, want)
			}
		}
	})

	t.Run("Anthropic Messages：adaptive thinking + output_config.effort", func(t *testing.T) {
		for effort, want := range map[ReasoningEffort]string{ReasoningEffortLow: "low", ReasoningEffortHigh: "high", ReasoningEffortMax: "max"} {
			srv, body := captureBody(t, anthropicSSE)
			client := anthropic.NewClient(option.WithAPIKey("test"), option.WithBaseURL(srv.URL))
			gen, err := NewAnthropicMessages(ctx, "p", AnthropicMessagesConfig{
				Model: "m", ReasoningEffort: effort,
				Limit: NewClientPool().OutputLimitFor(srv.URL, "test", "m", 4096),
			}, &client)
			if err != nil {
				t.Fatal(err)
			}
			// 开着思考时不下发温度：最新的模型在思考模式下拒绝非默认温度（400）。
			if _, err := gen.(OptionsGenerator).GenerateWithOptions(ctx, msgs, nil, GenerateOptions{Temperature: ptr(0.7)}); err != nil {
				t.Fatal(err)
			}
			got := body()
			thinking, _ := got["thinking"].(map[string]any)
			output, _ := got["output_config"].(map[string]any)
			if thinking["type"] != "adaptive" || output["effort"] != want {
				t.Errorf("思考深度 %q：thinking=%v output_config=%v，应为 adaptive + effort=%s", effort, got["thinking"], got["output_config"], want)
			}
			if _, has := got["temperature"]; has {
				t.Errorf("思考深度 %q：开着思考时不应下发 temperature", effort)
			}
		}
		// 没设置：两个参数都不发，温度照常下发
		srv, body := captureBody(t, anthropicSSE)
		client := anthropic.NewClient(option.WithAPIKey("test"), option.WithBaseURL(srv.URL))
		gen, err := NewAnthropicMessages(ctx, "p", AnthropicMessagesConfig{
			Model: "m", Limit: NewClientPool().OutputLimitFor(srv.URL, "test", "m", 4096),
		}, &client)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := gen.(OptionsGenerator).GenerateWithOptions(ctx, msgs, nil, GenerateOptions{Temperature: ptr(0.3)}); err != nil {
			t.Fatal(err)
		}
		got := body()
		if _, has := got["thinking"]; has {
			t.Errorf("没设置思考深度时不应下发 thinking: %v", got["thinking"])
		}
		if _, has := got["output_config"]; has {
			t.Errorf("没设置思考深度时不应下发 output_config: %v", got["output_config"])
		}
		if got["temperature"] != 0.3 {
			t.Errorf("没设置思考深度时温度照常下发，实际 %v", got["temperature"])
		}
	})
}

// 不在档位表里的取值在构造时就报出来，不发到上游去碰。
func TestReasoningEffort_RejectedAtConstruction(t *testing.T) {
	for name, build := range map[string]func() error{
		"Chat Completions": func() error {
			_, err := NewOpenAIChatCompletions(context.Background(), "p", OpenAIChatCompletionsConfig{
				Model: "m", ReasoningEffort: "xhigh",
				Limit: NewClientPool().OutputLimitFor("", "", "m", 4096),
			}, openai.NewClient("test"))
			return err
		},
		"Anthropic Messages": func() error {
			client := anthropic.NewClient(option.WithAPIKey("test"))
			_, err := NewAnthropicMessages(context.Background(), "p", AnthropicMessagesConfig{
				Model: "m", ReasoningEffort: "none",
				Limit: NewClientPool().OutputLimitFor("", "", "m", 4096),
			}, &client)
			return err
		},
	} {
		if err := build(); err == nil || !strings.Contains(err.Error(), "思考深度") {
			t.Errorf("%s：非法档位应报错，实际 %v", name, err)
		}
	}
}

func ptr[T any](v T) *T { return &v }
