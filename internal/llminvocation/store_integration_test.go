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

// TestStore_Append_Basic 验证：插一条普通 LLM 调用无错误。
func TestStore_Append_Basic(t *testing.T) {
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
		CostUSD:      0.00041,
		LatencyMs:    850,
		FinishReason: "stop",
	}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := s.Flush(ctx); err != nil {
		t.Fatalf("flush: %v", err)
	}

	// SumCostByOwner SQL 加 OR owner_id 兼容，传 owner_id 命中
	got, err := s.SumCostByOwnerID(ctx, oid)
	if err != nil {
		t.Fatalf("sum: %v", err)
	}
	if diff := got - 0.00041; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("sum 不匹配: got=%.9f want=0.00041", got)
	}
}

// TestStore_Append_WithRouteKey 验证：role 字段正确写入 + CountByRole 按 role 分组。
func TestStore_Append_WithRouteKey(t *testing.T) {
	ctx := context.Background()
	s, ot, oid := setup(t)

	if _, err := s.Append(ctx, Invocation{
		OwnerType:   &ot,
		OwnerID:     &oid,
		Provider:    "deepseek",
		Model:       "deepseek-chat",
		Role: "inspector",
		CostUSD:     0.0001,
	}); err != nil {
		t.Fatalf("append inspector: %v", err)
	}
	if _, err := s.Append(ctx, Invocation{
		OwnerType:   &ot,
		OwnerID:     &oid,
		Provider:    "deepseek",
		Model:       "deepseek-chat",
		Role: "inspector",
		CostUSD:     0.0002,
	}); err != nil {
		t.Fatalf("append inspector 2: %v", err)
	}
	if _, err := s.Append(ctx, Invocation{
		OwnerType:   &ot,
		OwnerID:     &oid,
		Provider:    "deepseek",
		Model:       "deepseek-reasoner",
		Role: "react_main",
		CostUSD:     0.001,
	}); err != nil {
		t.Fatalf("append react.main: %v", err)
	}

	if err := s.Flush(ctx); err != nil {
		t.Fatalf("flush: %v", err)
	}

	got, err := s.CountByRoleByOwnerID(ctx, oid)
	if err != nil {
		t.Fatalf("count by role: %v", err)
	}
	if got["inspector"] != 2 {
		t.Fatalf("inspector 应为 2, got %d (full=%v)", got["inspector"], got)
	}
	if got["react_main"] != 1 {
		t.Fatalf("react.main 应为 1, got %d (full=%v)", got["react_main"], got)
	}
}

// TestStore_SumCostByOwner 验证：插 3 条不同 role 的 call，sum 等于三者之和。
func TestStore_SumCostByOwnerID(t *testing.T) {
	ctx := context.Background()
	s, ot, oid := setup(t)

	costs := []struct {
		role string
		cost float64
	}{
		{"tracker", 0.001234},
		{"inspector", 0.000567},
		{"react_main", 0.002000},
	}
	var want float64
	for _, c := range costs {
		if _, err := s.Append(ctx, Invocation{
			OwnerType:   &ot,
			OwnerID:     &oid,
			Provider:    "deepseek",
			Model:       "deepseek-chat",
			Role: c.role,
			CostUSD:     c.cost,
		}); err != nil {
			t.Fatalf("append %s: %v", c.role, err)
		}
		want += c.cost
	}

	if err := s.Flush(ctx); err != nil {
		t.Fatalf("flush: %v", err)
	}

	got, err := s.SumCostByOwnerID(ctx, oid)
	if err != nil {
		t.Fatalf("sum cost: %v", err)
	}
	const eps = 1e-9
	if diff := got - want; diff > eps || diff < -eps {
		t.Fatalf("sum cost 不匹配: got=%.9f want=%.9f", got, want)
	}
}

// TestStore_SumCostByOwnerID_Empty 验证：无任何 call 时返回 0，不报错。
func TestStore_SumCostByOwnerID_Empty(t *testing.T) {
	ctx := context.Background()
	s, _, oid := setup(t)

	got, err := s.SumCostByOwnerID(ctx, oid)
	if err != nil {
		t.Fatalf("sum cost on empty: %v", err)
	}
	if got != 0 {
		t.Fatalf("空 owner sum 应为 0, got %.9f", got)
	}
}
