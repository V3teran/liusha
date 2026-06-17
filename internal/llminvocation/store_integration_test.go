//go:build integration

package llminvocation

import (
	"context"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/dbtest"
	"github.com/V3teran/liusha/internal/passivesession"
)

// setup 启动 Postgres、建 passive_session，返回 (Store, ownerType, ownerID)。
func setup(t *testing.T) (*Store, string, string) {
	t.Helper()
	pool := dbtest.NewPgPool(t)
	ps := passivesession.NewStore(pool)
	sess, err := ps.LookupOrCreate(context.Background(), "test.example.com", 24*time.Hour)
	if err != nil {
		t.Fatalf("create passive_session: %v", err)
	}
	return NewStoreWithConfig(pool, config.InvocationConfig{}), "passive_session", sess.ID
}

// TestStore_Append_ListByOwner 验证：插一条调用 → flush → 按 owner 读回，token/role 正确。
func TestStore_Append_ListByOwner(t *testing.T) {
	ctx := context.Background()
	s, ot, oid := setup(t)

	if _, err := s.Append(ctx, Invocation{
		OwnerType:    &ot,
		OwnerID:      &oid,
		Provider:     "deepseek",
		Model:        "deepseek-chat",
		InTokens:     1200,
		OutTokens:    300,
		CachedTokens: 100,
		LatencyMs:    850,
		FinishReason: "stop",
		Role:         "orchestrator",
	}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := s.Flush(ctx); err != nil {
		t.Fatalf("flush: %v", err)
	}

	rows, err := s.ListByOwner(ctx, "", oid)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("应读回 1 行，得到 %d", len(rows))
	}
	g := rows[0]
	if g.InTokens != 1200 || g.OutTokens != 300 || g.CachedTokens != 100 {
		t.Errorf("token 映射错: %+v", g)
	}
	if g.Role != "orchestrator" {
		t.Errorf("role 错: %q", g.Role)
	}
}

// TestStore_AggregateByOwner 验证：插多条 → 合计 token/latency/calls 等于各项之和。
func TestStore_AggregateByOwner(t *testing.T) {
	ctx := context.Background()
	s, ot, oid := setup(t)

	rows := []Invocation{
		{Provider: "deepseek", Model: "deepseek-chat", Role: "orchestrator", InTokens: 100, OutTokens: 10, CachedTokens: 5, LatencyMs: 200},
		{Provider: "deepseek", Model: "deepseek-chat", Role: "exploitation", InTokens: 200, OutTokens: 20, CachedTokens: 0, LatencyMs: 300},
		{Provider: "deepseek", Model: "deepseek-chat", Role: "exploitation", InTokens: 50, OutTokens: 5, CachedTokens: 50, LatencyMs: 100},
	}
	for i := range rows {
		rows[i].OwnerType = &ot
		rows[i].OwnerID = &oid
		if _, err := s.Append(ctx, rows[i]); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	if err := s.Flush(ctx); err != nil {
		t.Fatalf("flush: %v", err)
	}

	agg, err := s.AggregateByOwner(ctx, oid)
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if agg.Calls != 3 {
		t.Errorf("calls 应为 3, got %d", agg.Calls)
	}
	if agg.InTokens != 350 || agg.OutTokens != 35 || agg.CachedTokens != 55 {
		t.Errorf("token 合计错: %+v", agg)
	}
	if agg.LatencyMs != 600 {
		t.Errorf("latency 合计应为 600, got %d", agg.LatencyMs)
	}
}

// TestStore_AggregateByOwner_Empty 验证：无任何调用时返回零值，不报错。
func TestStore_AggregateByOwner_Empty(t *testing.T) {
	ctx := context.Background()
	s, _, oid := setup(t)

	agg, err := s.AggregateByOwner(ctx, oid)
	if err != nil {
		t.Fatalf("aggregate on empty: %v", err)
	}
	if agg.Calls != 0 || agg.InTokens != 0 || agg.LatencyMs != 0 {
		t.Fatalf("空 owner 应全零, got %+v", agg)
	}
}
