package einollm

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/config/llmcfg"
)

// fakeResolver 把 role 映射到 provider 部署（测试注入，替代真实 llmstore 的多级缓存解析）。
// 路由（role→别名→provider）的正确性由 llmstore 单测覆盖，这里只喂解析结果。
type fakeResolver struct {
	byRole map[string]llmcfg.Provider
	def    llmcfg.Provider
	hasDef bool
}

func (r fakeResolver) ProviderForRole(_ context.Context, role string) (llmcfg.Provider, error) {
	if p, ok := r.byRole[role]; ok {
		return p, nil
	}
	if r.hasDef {
		return r.def, nil
	}
	return llmcfg.Provider{}, errors.New("unresolved role " + role)
}

// testFactory：orchestrator→小米 openai_compat；default 兜底同小米。
func testFactory() *Factory {
	xiaomi := llmcfg.Provider{
		Key:          "xiaomi_mimo",
		Type:         "openai_compat",
		BaseURL:      "https://token-plan-cn.xiaomimimo.com/v1",
		DefaultModel: "mimo-v2.5",
		APIKeyEnv:    "TEST_XIAOMI_KEY",
	}
	r := fakeResolver{
		byRole: map[string]llmcfg.Provider{"orchestrator": xiaomi},
		def:    xiaomi,
		hasDef: true,
	}
	return New(r, config.Config{})
}

// For 对 openai_compat provider 构造原生 eino ChatModel（构造不发网络请求，dummy key 即可）。
func TestFor_BuildsOpenAICompat(t *testing.T) {
	t.Setenv("TEST_XIAOMI_KEY", "tp-dummy-for-construct")
	cm, err := testFactory().For(context.Background(), "orchestrator")
	if err != nil {
		t.Fatalf("For(orchestrator) 报错: %v", err)
	}
	if cm == nil {
		t.Fatal("For 返回 nil ChatModel")
	}
}

// anthropic 暂未接入，返回明确 TODO 错误（不静默）。
func TestFor_AnthropicNotYet(t *testing.T) {
	t.Setenv("TEST_CLAUDE_KEY", "sk-dummy")
	claude := llmcfg.Provider{Key: "claude", Type: "anthropic", APIKeyEnv: "TEST_CLAUDE_KEY"}
	r := fakeResolver{byRole: map[string]llmcfg.Provider{"x": claude}}
	_, err := New(r, config.Config{}).For(context.Background(), "x")
	if err == nil || !strings.Contains(err.Error(), "anthropic 尚未接入") {
		t.Fatalf("anthropic 应返回未接入错误，得到: %v", err)
	}
}

// 缺 api key env → 明确报错。
func TestFor_MissingKey(t *testing.T) {
	_, err := testFactory().For(context.Background(), "orchestrator")
	if err == nil || !strings.Contains(err.Error(), "为空") {
		t.Fatalf("缺 key 应报错，得到: %v", err)
	}
}

// resolver 解析失败（路由未配置）时 For 透传错误，不静默兜底。
func TestFor_ResolveError(t *testing.T) {
	r := fakeResolver{byRole: map[string]llmcfg.Provider{}}
	if _, err := New(r, config.Config{}).For(context.Background(), "anything"); err == nil {
		t.Fatal("resolver 解析失败时 For 应报错")
	}
}
