//go:build integration

package llmstore

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/V3teran/liusha/internal/cachestore"
	llmcfg "github.com/V3teran/liusha/internal/config/llm"
	"github.com/V3teran/liusha/internal/dbtest"
)

// TestProviderCodeRoundtrip 一次性验证 0159（llm_provider.key→code）后存储/缓存/路由链路。
func TestProviderCodeRoundtrip(t *testing.T) {
	pool := dbtest.NewPgPool(t)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	s := New(pool, cachestore.New(rdb, 0))

	if _, err := s.SaveProvider(context.Background(), llmcfg.ProviderParams{
		Code: "deepseek", Type: "openai_compat", BaseURL: "https://api.deepseek.com",
		DefaultModel: "deepseek-chat", MaxTokens: 4096, SupportsTools: true,
		ContextWindow: 64000,
	}); err != nil {
		t.Fatalf("save provider: %v", err)
	}

	got, err := s.ProviderByCode(context.Background(), "deepseek")
	if err != nil {
		t.Fatalf("by code: %v", err)
	}
	if got.Code != "deepseek" || got.DefaultModel != "deepseek-chat" {
		t.Fatalf("provider = %+v", got)
	}

	if _, err := s.UpsertRoleRoute(context.Background(), "planner", "deepseek"); err != nil {
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
