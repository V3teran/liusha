package einollm

import (
	"context"
	"strings"
	"testing"

	"github.com/V3teran/liusha/internal/config"
)

// 内存合成 config（不依赖 config.Load 的全量校验）：小米 openai_compat + anthropic。
func testCfg() config.Config {
	return config.Config{
		Providers: map[string]config.ProviderConfig{
			"xiaomi_mimo": {
				Type:         "openai_compat",
				BaseURL:      "https://token-plan-cn.xiaomimimo.com/v1",
				DefaultModel: "mimo-v2.5",
				APIKeyEnv:    "TEST_XIAOMI_KEY",
			},
			"claude": {
				Type:      "anthropic",
				APIKeyEnv: "TEST_CLAUDE_KEY",
			},
		},
		LLM: config.LLMConfig{
			DefaultProvider: "xiaomi_mimo",
			Agents:          map[string]string{"commander": "default_provider"},
			Utilities:       map[string]string{"compactor": "light_provider"},
			LightProvider:   "xiaomi_mimo",
		},
	}
}

// resolveProviderKey 沿用 liusha role→provider 语义。
func TestResolveProviderKey(t *testing.T) {
	f := New(testCfg())
	cases := []struct{ role, want string }{
		{"commander", "xiaomi_mimo"},    // Agents → default_provider
		{"compactor", "xiaomi_mimo"},    // Utilities → light_provider
		{"unknown_role", "xiaomi_mimo"}, // 未配置 → default_provider
	}
	for _, c := range cases {
		if got := f.resolveProviderKey(c.role); got != c.want {
			t.Errorf("resolveProviderKey(%q)=%q, want %q", c.role, got, c.want)
		}
	}
}

// For 对 openai_compat provider 构造原生 eino ChatModel（构造不发网络请求，dummy key 即可）。
func TestFor_BuildsOpenAICompat(t *testing.T) {
	t.Setenv("TEST_XIAOMI_KEY", "tp-dummy-for-construct")
	cm, err := New(testCfg()).For(context.Background(), "commander")
	if err != nil {
		t.Fatalf("For(commander) 报错: %v", err)
	}
	if cm == nil {
		t.Fatal("For 返回 nil ChatModel")
	}
}

// anthropic 暂未接入，返回明确 TODO 错误（不静默）。
func TestFor_AnthropicNotYet(t *testing.T) {
	t.Setenv("TEST_CLAUDE_KEY", "sk-dummy")
	cfg := testCfg()
	cfg.LLM.DefaultProvider = "claude"
	_, err := New(cfg).For(context.Background(), "x")
	if err == nil || !strings.Contains(err.Error(), "anthropic 尚未接入") {
		t.Fatalf("anthropic 应返回未接入错误，得到: %v", err)
	}
}

// 缺 api key env → 明确报错。
func TestFor_MissingKey(t *testing.T) {
	_, err := New(testCfg()).For(context.Background(), "commander")
	if err == nil || !strings.Contains(err.Error(), "为空") {
		t.Fatalf("缺 key 应报错，得到: %v", err)
	}
}
