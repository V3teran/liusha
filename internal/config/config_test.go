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
  default_provider: deepseek
  vision_provider: anthropic
  max_steps: 30
  max_tokens_per_call: 4096
providers:
  deepseek:  {base_url: https://api.deepseek.com,  default_model: deepseek-chat,    api_key_env: DEEPSEEK_API_KEY,  max_tokens: 4096, supports_tools: true,  supports_vision: false, context_window: 65536}
  anthropic: {base_url: https://api.anthropic.com, default_model: claude-sonnet-4-6, vision_model: claude-haiku-4-5, api_key_env: ANTHROPIC_API_KEY, max_tokens: 8192, supports_tools: true, supports_vision: true,  context_window: 200000}
proxy: {window_batch: 20, window_max_age_seconds: 30, allow_hosts: [vulnapp]}
session: {sweeper_interval_seconds: 600}
skills: {root: ./skills}
`

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoad_MissingDefaultProviderKey(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "k-anth")
	if _, err := Load(writeConfig(t, minimalYAML)); err == nil {
		t.Fatalf("expected error when DEEPSEEK_API_KEY empty")
	}
}

func TestLoad_OK(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "k-deep")
	t.Setenv("ANTHROPIC_API_KEY", "k-anth")
	cfg, err := Load(writeConfig(t, minimalYAML))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLM.DefaultProvider != "deepseek" || cfg.Providers["deepseek"].DefaultModel != "deepseek-chat" {
		t.Fatalf("unexpected: %+v", cfg)
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
