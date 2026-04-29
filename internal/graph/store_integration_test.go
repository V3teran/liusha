//go:build integration

package graph

import (
	"context"
	"encoding/json"
	"strings"
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

// TestStore_UpsertNode_Idempotent 验证：同 (engagement_id, kind, dedup_key) 第二次 upsert
// 不会重复插入，返回同一 ID，且 payload 通过 jsonb || EXCLUDED.payload 合并。
func TestStore_UpsertNode_Idempotent(t *testing.T) {
	ctx := context.Background()
	s, eid := setup(t)

	a, err := s.UpsertNode(ctx, NodeParams{
		EngagementID: eid,
		Kind:         "endpoint",
		DedupKey:     "vulnapp:GET:/api/x",
		Payload:      json.RawMessage(`{"method":"GET","seen":1}`),
	})
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	b, err := s.UpsertNode(ctx, NodeParams{
		EngagementID: eid,
		Kind:         "endpoint",
		DedupKey:     "vulnapp:GET:/api/x",
		Payload:      json.RawMessage(`{"seen":2,"hits":3}`),
	})
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	if a.ID != b.ID {
		t.Fatalf("UNIQUE 失效，应返回同一 ID, got %s vs %s", a.ID, b.ID)
	}

	got, err := s.GetNode(ctx, a.ID)
	if err != nil {
		t.Fatalf("get node: %v", err)
	}
	var p map[string]any
	if err := json.Unmarshal(got.Payload, &p); err != nil {
		t.Fatalf("payload json invalid: %v", err)
	}
	// 第一次 method=GET 保留，seen 被第二次覆盖为 2，hits 为新增。
	if p["method"] != "GET" {
		t.Fatalf("payload 未保留旧字段 method=GET: %+v", p)
	}
	if v, _ := p["seen"].(float64); v != 2 {
		t.Fatalf("payload seen 应被合并为 2, got %v", p["seen"])
	}
	if v, _ := p["hits"].(float64); v != 3 {
		t.Fatalf("payload 未合并新字段 hits=3: %+v", p)
	}
}

// TestStore_UpsertNode_DifferentDedupKeyDistinct 验证：不同 dedup_key 是不同的 node 行。
func TestStore_UpsertNode_DifferentDedupKeyDistinct(t *testing.T) {
	ctx := context.Background()
	s, eid := setup(t)

	a, _ := s.UpsertNode(ctx, NodeParams{EngagementID: eid, Kind: "endpoint", DedupKey: "k1"})
	b, _ := s.UpsertNode(ctx, NodeParams{EngagementID: eid, Kind: "endpoint", DedupKey: "k2"})
	if a.ID == b.ID {
		t.Fatalf("不同 dedup_key 应得到不同 node, got 同 ID %s", a.ID)
	}

	list, err := s.ListNodesByEngagement(ctx, eid, 10)
	if err != nil {
		t.Fatalf("list nodes: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("应有 2 个 node, got %d", len(list))
	}
}

// TestStore_UpsertEdge_Idempotent 验证：同 (engagement_id, from_id, to_id, kind) upsert 幂等，
// 且 payload 在第二次 upsert 时被 jsonb || 合并。
func TestStore_UpsertEdge_Idempotent(t *testing.T) {
	ctx := context.Background()
	s, eid := setup(t)

	from, _ := s.UpsertNode(ctx, NodeParams{EngagementID: eid, Kind: "endpoint", DedupKey: "from"})
	to, _ := s.UpsertNode(ctx, NodeParams{EngagementID: eid, Kind: "endpoint", DedupKey: "to"})

	e1, err := s.UpsertEdge(ctx, EdgeParams{
		EngagementID: eid,
		FromID:       from.ID,
		ToID:         to.ID,
		Kind:         "calls",
		Payload:      json.RawMessage(`{"count":1}`),
	})
	if err != nil {
		t.Fatalf("first upsert edge: %v", err)
	}
	e2, err := s.UpsertEdge(ctx, EdgeParams{
		EngagementID: eid,
		FromID:       from.ID,
		ToID:         to.ID,
		Kind:         "calls",
		Payload:      json.RawMessage(`{"count":5,"latest":true}`),
	})
	if err != nil {
		t.Fatalf("second upsert edge: %v", err)
	}
	if e1.ID != e2.ID {
		t.Fatalf("edge UNIQUE 失效, got %s vs %s", e1.ID, e2.ID)
	}

	var p map[string]any
	if err := json.Unmarshal(e2.Payload, &p); err != nil {
		t.Fatalf("payload json invalid: %v", err)
	}
	if v, _ := p["count"].(float64); v != 5 {
		t.Fatalf("edge payload count 应被合并为 5, got %v", p["count"])
	}
	if v, _ := p["latest"].(bool); !v {
		t.Fatalf("edge payload 未合并 latest: %+v", p)
	}
}

// TestStore_UpsertEdge_RequiresExistingNodes 验证：from_id/to_id 必须是已存在的 node（FK 约束）。
func TestStore_UpsertEdge_RequiresExistingNodes(t *testing.T) {
	ctx := context.Background()
	s, eid := setup(t)

	// 用合法格式但不存在的 uuid，FK 应该报错。
	const ghost = "00000000-0000-0000-0000-000000000000"
	_, err := s.UpsertEdge(ctx, EdgeParams{
		EngagementID: eid,
		FromID:       ghost,
		ToID:         ghost,
		Kind:         "calls",
	})
	if err == nil {
		t.Fatalf("FK 约束失效：from_id/to_id 不存在时应报错")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "foreign") &&
		!strings.Contains(strings.ToLower(err.Error()), "violat") {
		t.Logf("非 FK 错误也接受: %v", err)
	}
}

// TestStore_ListEdgesByEngagement 验证：list 返回 engagement 范围内全部 edge。
func TestStore_ListEdgesByEngagement(t *testing.T) {
	ctx := context.Background()
	s, eid := setup(t)

	a, _ := s.UpsertNode(ctx, NodeParams{EngagementID: eid, Kind: "endpoint", DedupKey: "a"})
	b, _ := s.UpsertNode(ctx, NodeParams{EngagementID: eid, Kind: "endpoint", DedupKey: "b"})
	c, _ := s.UpsertNode(ctx, NodeParams{EngagementID: eid, Kind: "endpoint", DedupKey: "c"})

	if _, err := s.UpsertEdge(ctx, EdgeParams{EngagementID: eid, FromID: a.ID, ToID: b.ID, Kind: "calls"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertEdge(ctx, EdgeParams{EngagementID: eid, FromID: a.ID, ToID: c.ID, Kind: "calls"}); err != nil {
		t.Fatal(err)
	}

	list, err := s.ListEdgesByEngagement(ctx, eid, 10)
	if err != nil {
		t.Fatalf("list edges: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("应有 2 条 edge, got %d", len(list))
	}
}
