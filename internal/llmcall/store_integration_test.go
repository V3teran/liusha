//go:build integration

package llmcall

import (
	"context"
	"testing"

	"github.com/V3teran/liusha/internal/dbtest"
	"github.com/V3teran/liusha/internal/engagement"
)

// setup 启动 Postgres、懒创建 engagement，返回 (Store, engagementID)。
func setup(t *testing.T) (*Store, string) {
	t.Helper()
	pool := dbtest.NewPgPool(t)
	es := engagement.NewStore(pool)
	e, err := es.LookupOrCreate(context.Background(), "default", "h", engagement.ModeProxy)
	if err != nil {
		t.Fatalf("lookup engagement: %v", err)
	}
	return NewStore(pool), e.ID
}

// TestStore_Append_Basic 验证：插一条普通 LLM 调用，返回的 id 非 0。
func TestStore_Append_Basic(t *testing.T) {
	ctx := context.Background()
	s, eid := setup(t)

	id, err := s.Append(ctx, Call{
		EngagementID: &eid,
		Provider:     "deepseek",
		Model:        "deepseek-chat",
		InTokens:     1200,
		OutTokens:    300,
		CachedTokens: 100,
		CostUSD:      0.00041,
		LatencyMs:    850,
		FinishReason: "stop",
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if id == 0 {
		t.Fatalf("append 返回 id=0, 应为非零")
	}
}

// TestStore_Append_WithRouteKey 验证：role 字段正确写入 + CountByRole 按 role 分组。
// 黑客松借鉴：T21 Instrument 的 RouteKey 维度（react.main / observer / distill / compaction / vision）。
func TestStore_Append_WithRouteKey(t *testing.T) {
	ctx := context.Background()
	s, eid := setup(t)

	if _, err := s.Append(ctx, Call{
		EngagementID: &eid,
		Provider:     "deepseek",
		Model:        "deepseek-chat",
		Role:         "observer",
		CostUSD:      0.0001,
	}); err != nil {
		t.Fatalf("append observer: %v", err)
	}
	if _, err := s.Append(ctx, Call{
		EngagementID: &eid,
		Provider:     "deepseek",
		Model:        "deepseek-chat",
		Role:         "observer",
		CostUSD:      0.0002,
	}); err != nil {
		t.Fatalf("append observer 2: %v", err)
	}
	if _, err := s.Append(ctx, Call{
		EngagementID: &eid,
		Provider:     "deepseek",
		Model:        "deepseek-reasoner",
		Role:         "react_main",
		CostUSD:      0.001,
	}); err != nil {
		t.Fatalf("append react.main: %v", err)
	}

	got, err := s.CountByRole(ctx, eid)
	if err != nil {
		t.Fatalf("count by role: %v", err)
	}
	if got["observer"] != 2 {
		t.Fatalf("observer 应为 2, got %d (full=%v)", got["observer"], got)
	}
	if got["react_main"] != 1 {
		t.Fatalf("react.main 应为 1, got %d (full=%v)", got["react_main"], got)
	}
}

// TestStore_SumCostByEngagement 验证：插 3 条不同 role 的 call，sum 等于三者之和。
func TestStore_SumCostByEngagement(t *testing.T) {
	ctx := context.Background()
	s, eid := setup(t)

	costs := []struct {
		role string
		cost float64
	}{
		{"react_main", 0.001234},
		{"observer", 0.000567},
		{"distill", 0.002000},
	}
	var want float64
	for _, c := range costs {
		if _, err := s.Append(ctx, Call{
			EngagementID: &eid,
			Provider:     "deepseek",
			Model:        "deepseek-chat",
			Role:         c.role,
			CostUSD:      c.cost,
		}); err != nil {
			t.Fatalf("append %s: %v", c.role, err)
		}
		want += c.cost
	}

	got, err := s.SumCostByEngagement(ctx, eid)
	if err != nil {
		t.Fatalf("sum cost: %v", err)
	}
	// numeric(12,6) -> float64：6 位小数足够精确，对比时给一点误差容差。
	const eps = 1e-9
	if diff := got - want; diff > eps || diff < -eps {
		t.Fatalf("sum cost 不匹配: got=%.9f want=%.9f", got, want)
	}
}

// TestStore_SumCostByEngagement_Empty 验证：无任何 call 时返回 0，不报错。
func TestStore_SumCostByEngagement_Empty(t *testing.T) {
	ctx := context.Background()
	s, eid := setup(t)

	got, err := s.SumCostByEngagement(ctx, eid)
	if err != nil {
		t.Fatalf("sum cost on empty: %v", err)
	}
	if got != 0 {
		t.Fatalf("空 engagement sum 应为 0, got %.9f", got)
	}
}
