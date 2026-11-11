//go:build integration

package llmstore

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/V3teran/liusha/internal/cachestore"
	"github.com/V3teran/liusha/internal/dbtest"
	llmconfig "github.com/V3teran/liusha/internal/llmconfig"
)

// TestProviderCodeRoundtrip 验证 provider code 在存储 / 缓存 / 路由链路上的往返一致。
func TestProviderCodeRoundtrip(t *testing.T) {
	pool := dbtest.NewPgPool(t)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	s := New(pool, cachestore.New(rdb, 0))

	if _, err := s.CreateProvider(context.Background(), llmconfig.ProviderParams{
		Code: "deepseek", DisplayName: "deepseek", Type: "openai_compat", BaseURL: "https://api.deepseek.com",
		DefaultModel: "deepseek-chat", MaxTokens: 4096, SupportsTools: true,
		ContextWindow: 64000,
	}); err != nil {
		t.Fatalf("create provider: %v", err)
	}

	got, err := s.ProviderByCode(context.Background(), "deepseek")
	if err != nil {
		t.Fatalf("by code: %v", err)
	}
	if got.Code != "deepseek" || got.DefaultModel != "deepseek-chat" {
		t.Fatalf("provider = %+v", got)
	}

	if _, err := s.UpsertRoleRoute(context.Background(), llmconfig.ComplexityComplex, "deepseek"); err != nil {
		t.Fatalf("upsert role route: %v", err)
	}
	routes, err := s.ListRoleRoutes(context.Background())
	if err != nil {
		t.Fatalf("routes: %v", err)
	}
	if len(routes) != 1 || routes[0].ProviderCode != "deepseek" {
		t.Fatalf("routes = %+v", routes)
	}
}
