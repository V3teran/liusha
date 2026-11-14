//go:build integration

package sandbox

import (
	"context"
	"slices"
	"testing"

	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/dbtest"
	"github.com/V3teran/liusha/internal/llmconfig"
)

// provider 的密钥变量名由首装种子落库，之后配置里可能不再声明，而变量仍在进程环境里：
// 清理名单必须含数据库里全部 provider 声明的变量（含停用的）。
func TestSecretEnv_IncludesProviderKeyVarsFromDatabase(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	store := llmconfig.NewStore(pool)
	for _, p := range []llmconfig.ProviderParams{
		{Code: "a", DisplayName: "甲", APIKeyEnv: "ACME_API_KEY", Enabled: true},
		{Code: "b", DisplayName: "乙", APIKeyEnv: "OTHER_LLM_KEY", Enabled: false},
	} {
		p.Type, p.BaseURL, p.DefaultModel = llmconfig.ProviderTypeAnthropic, "https://llm.example.com", "m"
		p.ContextWindow, p.MaxTokens = 1000, 1024
		if _, err := store.CreateProvider(ctx, p); err != nil {
			t.Fatalf("create provider: %v", err)
		}
	}

	var cfg config.AppConfig
	cfg.Postgres.DSN = pool.Config().ConnString()
	names, err := secretEnv(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ACME_API_KEY", "OTHER_LLM_KEY", "JINA_API_KEY"} {
		if !slices.Contains(names, want) {
			t.Errorf("清理名单缺 %s：%v", want, names)
		}
	}

	// 配置了数据库却读不到：拒绝启动，而不是带着残缺名单运行。
	cfg.Postgres.DSN = "postgres://nobody@127.0.0.1:1/none?sslmode=disable&connect_timeout=1"
	if _, err := secretEnv(ctx, cfg); err == nil {
		t.Fatal("数据库不可读时应拒绝启动")
	}
}
