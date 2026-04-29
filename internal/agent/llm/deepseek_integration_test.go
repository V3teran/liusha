//go:build integration

package llm

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
)

// 默认 endpoint + model；可通过 env 覆盖（CI/本地灵活切换）。
const (
	defaultDeepSeekBaseURL = "https://api.deepseek.com/"
	defaultDeepSeekModel   = "deepseek-chat"
)

// requireDeepSeek 跳过守护：无 key 自动跳过。
func requireDeepSeek(t *testing.T) DeepSeekConfig {
	t.Helper()
	key := os.Getenv("DEEPSEEK_API_KEY")
	if key == "" {
		t.Skip("跳过：环境未提供 DEEPSEEK_API_KEY")
	}
	baseURL := os.Getenv("DEEPSEEK_BASE_URL")
	if baseURL == "" {
		baseURL = defaultDeepSeekBaseURL
	}
	model := os.Getenv("DEEPSEEK_MODEL")
	if model == "" {
		model = defaultDeepSeekModel
	}
	return DeepSeekConfig{
		BaseURL:   baseURL,
		Model:     model,
		APIKey:    key,
		MaxTokens: 256,
	}
}

// TestDeepSeek_Generate 真实调用 DeepSeek，断言 Content 非空且 OutTokens > 0
func TestDeepSeek_Generate(t *testing.T) {
	cfg := requireDeepSeek(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	gen, err := NewDeepSeek(ctx, cfg, nil)
	if err != nil {
		t.Fatalf("NewDeepSeek 失败: %v", err)
	}

	if gen.Provider() != "deepseek" {
		t.Errorf("Provider 应为 deepseek，实际 %s", gen.Provider())
	}
	if gen.Model() != cfg.Model {
		t.Errorf("Model 应为 %s，实际 %s", cfg.Model, gen.Model())
	}

	res, err := gen.Generate(ctx, []Message{
		{Role: RoleUser, Content: "hello"},
	}, nil)
	if err != nil {
		t.Fatalf("Generate 失败: %v", err)
	}
	if res.Content == "" {
		t.Errorf("Content 不应为空")
	}
	if res.Usage.OutTokens <= 0 {
		t.Errorf("OutTokens 应 > 0，实际 %d", res.Usage.OutTokens)
	}
	t.Logf("结果: content=%q usage=%+v finish=%s", res.Content, res.Usage, res.FinishReason)
}

// TestDeepSeek_GenerateWithTool 绑定一个 weather 工具；只断言调用不报错且 Result 合法
func TestDeepSeek_GenerateWithTool(t *testing.T) {
	cfg := requireDeepSeek(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	weatherSchema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"city": {"type": "string", "description": "城市名"}
		},
		"required": ["city"]
	}`)

	gen, err := NewDeepSeek(ctx, cfg, []ToolSchema{{
		Name:        "get_weather",
		Description: "获取指定城市的天气",
		Parameters:  weatherSchema,
	}})
	if err != nil {
		t.Fatalf("NewDeepSeek 失败: %v", err)
	}

	res, err := gen.Generate(ctx, []Message{
		{Role: RoleUser, Content: "北京今天天气怎么样？"},
	}, nil)
	if err != nil {
		t.Fatalf("Generate 失败: %v", err)
	}

	// 模型可能直接调工具，也可能只回文本——只校验返回结构合法
	if res.Content == "" && len(res.ToolCalls) == 0 {
		t.Errorf("Content 与 ToolCalls 不应同时为空")
	}
	for _, tc := range res.ToolCalls {
		if tc.Name == "" {
			t.Errorf("ToolCall.Name 不应为空")
		}
	}
	t.Logf("结果: content=%q toolcalls=%d usage=%+v", res.Content, len(res.ToolCalls), res.Usage)
}
