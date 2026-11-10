//go:build integration

package llmconfig

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/V3teran/liusha/internal/dbtest"
)

// TestStoreSemanticsPostgres 锁定 llm_provider / llm_role_route 写路径的错误语义：
// 重复新建 → ErrConflict；更新/删除/清密钥的「不存在」→ 包装 pgx.ErrNoRows；
// 无环境变量可回落时清除已存密钥 → ErrConflict；非法档位 → ErrInvalid。
func TestStoreSemanticsPostgres(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过集成测试")
	}
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	defer pool.Close()
	for _, q := range []string{"DELETE FROM llm_role_route", "DELETE FROM llm_provider"} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("清理: %v", err)
		}
	}
	s := NewStore(pool)

	keyed := ProviderParams{
		Code: "keyed", DisplayName: "主力", Type: ProviderTypeAnthropic, BaseURL: "https://api.example.com",
		DefaultModel: "m", ContextWindow: 1000, MaxTokens: 8192, ThinkingBudgetTokens: 2048,
		EncryptedAPIKey: []byte("sealed"), APIKeyLast4: "abcd", Enabled: true,
	}
	created, err := s.CreateProvider(ctx, keyed)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ThinkingBudgetTokens != 2048 {
		t.Fatalf("思考预算应落库: %+v", created)
	}
	if created.AuthScheme != AuthXAPIKey || created.OutputLimitParam != OutputLimitMaxTokens {
		t.Fatalf("请求画像应按协议缺省落库: %+v", created)
	}
	if _, err := s.CreateProvider(ctx, keyed); !errors.Is(err, ErrConflict) {
		t.Fatalf("重复新建应 ErrConflict，got %v", err)
	}
	// 显示名称不区分大小写唯一：新建、改名撞上已有名称都是 ErrConflict，且说明是名称冲突
	other := keyed
	other.Code, other.DisplayName = "other", " 主力 "
	if _, err := s.CreateProvider(ctx, other); !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "显示名称") {
		t.Fatalf("同名新建应 ErrConflict（名称冲突），got %v", err)
	}
	other.DisplayName = "备用"
	if _, err := s.CreateProvider(ctx, other); err != nil {
		t.Fatalf("不同名应可新建: %v", err)
	}
	other.KeepExistingKey, other.DisplayName = true, "主力"
	if _, err := s.UpdateProvider(ctx, other); !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "显示名称") {
		t.Fatalf("改名撞上已有名称应 ErrConflict，got %v", err)
	}
	if err := s.DeleteProvider(ctx, "other"); err != nil {
		t.Fatalf("清理 other: %v", err)
	}

	update := keyed
	update.KeepExistingKey, update.EncryptedAPIKey, update.APIKeyLast4 = true, nil, ""
	update.ThinkingBudgetTokens = 3000
	update.AuthScheme = AuthBearer
	update.BaseURL = "https://api.example.com/v1/messages/"
	updated, err := s.UpdateProvider(ctx, update)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.AuthScheme != AuthBearer || updated.BaseURL != "https://api.example.com" {
		t.Fatalf("协商出的鉴权方式应落库、Base URL 应归一: %+v", updated)
	}
	if updated.ThinkingBudgetTokens != 3000 || string(updated.EncryptedAPIKey) != "sealed" {
		t.Fatalf("更新应改思考预算、保留已存密钥: %+v", updated)
	}
	ghost := update
	ghost.Code = "ghost"
	if _, err := s.UpdateProvider(ctx, ghost); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("更新不存在应包装 ErrNoRows，got %v", err)
	}

	if _, err := s.ClearProviderKey(ctx, "keyed"); !errors.Is(err, ErrConflict) {
		t.Fatalf("无环境变量时清除密钥应 ErrConflict，got %v", err)
	}
	if p, _ := s.GetProvider(ctx, "keyed"); string(p.EncryptedAPIKey) != "sealed" {
		t.Fatal("被拒绝的清除不应动已存密钥")
	}
	if _, err := s.ClearProviderKey(ctx, "ghost"); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("清除不存在 provider 的密钥应包装 ErrNoRows，got %v", err)
	}
	if err := s.DeleteProvider(ctx, "ghost"); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("删除不存在应包装 ErrNoRows，got %v", err)
	}

	if _, err := s.UpsertRoleRoute(ctx, "planner", "keyed"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("非法档位应 ErrInvalid，got %v", err)
	}
	if _, err := s.UpsertRoleRoute(ctx, ComplexityComplex, "keyed"); err != nil {
		t.Fatalf("合法档位: %v", err)
	}

	if _, err := s.CreateProvider(ctx, ProviderParams{Code: "bad", Type: ProviderTypeOpenAICompat, BaseURL: "u", DefaultModel: "m", ContextWindow: 1}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("落库前同样校验，got %v", err)
	}
}
