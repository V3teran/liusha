package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

const minimalYAML = `
api: {read_timeout_seconds: 15, write_timeout_seconds: 30}
postgres: {max_conns: 20, min_conns: 2}
llm:
  complexity_routes:
    simple: deepseek
    medium: deepseek
    complex: anthropic
providers:
  deepseek:  {base_url: https://api.deepseek.com,  default_model: deepseek-chat,  api_key_env: DEEPSEEK_API_KEY,  max_tokens: 4096, supports_tools: true,  supports_vision: false, context_window: 65536}
  anthropic: {base_url: https://api.anthropic.com, default_model: claude-sonnet-4-6, api_key_env: ANTHROPIC_API_KEY, max_tokens: 8192, supports_tools: true, supports_vision: true,  context_window: 200000}
`

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestLoad_PartialKeyEnvBoots：部分 provider 密钥缺失不拒启——缺档由运行期
// 就近降级接管（complex 缺→medium→simple 等）；仅保留形状校验 fatal。
func TestLoad_PartialKeyEnvBoots(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "k-anth")
	if _, err := Load(writeConfig(t, minimalYAML)); err != nil {
		t.Fatalf("部分密钥缺失应可启动（就近降级）: %v", err)
	}
}

// TestLoad_AllKeysMissingBoots：全部路由档位密钥缺失不拒启——「先部署、后在前端
// 配置」的启动期体现；就绪由下发边界拒绝与运行期致命化接管。
func TestLoad_AllKeysMissingBoots(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	c, err := Load(writeConfig(t, minimalYAML))
	if err != nil {
		t.Fatalf("全部密钥缺失应可启动: %v", err)
	}
	if len(c.Providers) == 0 {
		t.Fatal("providers 应照常加载（就绪判定在运行期）")
	}
}

func TestLoad_OK(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "k-deep")
	t.Setenv("ANTHROPIC_API_KEY", "k-anth")
	cfg, err := Load(writeConfig(t, minimalYAML))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLM.ComplexityRoutes["medium"] != "deepseek" || cfg.Providers["deepseek"].DefaultModel != "deepseek-chat" {
		t.Fatalf("unexpected: %+v", cfg)
	}
}

// TestLoad_EmptyProvidersRelyOnDB：providers 留空时事实源在 DB，Load 不应 fail-fast
// 强制 default_provider——空 yaml + 满 DB 的正常部署必须能启动（见 validateLLMSeed）。
func TestLoad_EmptyProvidersRelyOnDB(t *testing.T) {
	const noProvidersYAML = `
api: {read_timeout_seconds: 15, write_timeout_seconds: 30}
postgres: {max_conns: 20, min_conns: 2}
`
	cfg, err := Load(writeConfig(t, noProvidersYAML))
	if err != nil {
		t.Fatalf("providers 留空应放行（DB 为事实源），got err: %v", err)
	}
	if len(cfg.Providers) != 0 {
		t.Fatalf("期望空 providers，got %+v", cfg.Providers)
	}
}

func TestApplyIngestorDefaults_ConsumerNameIsInstanceUnique(t *testing.T) {
	// consumer_name 留空时应派生每实例唯一名，绝不能退回写死的 ingestor-1
	// （多副本用同名进同组会静默 pending 混乱）。
	got := applyIngestorDefaults(IngestorConfig{}).ConsumerName

	if got == "ingestor-1" {
		t.Fatalf("consumer name 仍是写死的 ingestor-1，多副本会撞名")
	}
	host, _ := os.Hostname()
	want := "ingestor-" + host + "-" + fmt.Sprint(os.Getpid())
	if got != want {
		t.Fatalf("consumer name = %q, 期望派生自 hostname+pid = %q", got, want)
	}

	// 显式配置应被尊重，不被默认值覆盖。
	explicit := applyIngestorDefaults(IngestorConfig{ConsumerName: "custom-name"}).ConsumerName
	if explicit != "custom-name" {
		t.Fatalf("显式 consumer_name 被覆盖: %q", explicit)
	}
}
