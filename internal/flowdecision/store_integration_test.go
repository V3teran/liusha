//go:build integration

package flowdecision

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/V3teran/liusha/internal/dbtest"
	"github.com/V3teran/liusha/internal/engagement"
	"github.com/V3teran/liusha/internal/flow"
)

// TestStore_Append_Roundtrip 写一条 flow_decision 后用 ListByEngagement 拉回，
// 断言所有字段往返完整、jsonb 字段保留 LLM 原结构。
func TestStore_Append_Roundtrip(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)

	es := engagement.NewStore(pool)
	e, err := es.LookupOrCreate(ctx, "default", "h", engagement.ModeProxy)
	if err != nil {
		t.Fatal(err)
	}
	fs := flow.NewStore(pool, 0, 0)
	fid, err := fs.Append(ctx, flow.Flow{
		EngagementID: e.ID,
		Method:       "GET",
		URL:          "https://example.com/api/users/42",
		StatusCode:   200,
	})
	if err != nil {
		t.Fatal(err)
	}

	s := NewStore(pool)
	got, err := s.Append(ctx, Decision{
		EngagementID:        e.ID,
		FlowID:              fid,
		Operation:           "read",
		ResourceScope:       ResourceScopePrivate,
		AttackSurfaces:      json.RawMessage(`["json","query"]`),
		CarriesAuth:         true,
		CredentialLocations: json.RawMessage(`[{"type":"headers","key":"Cookie"}]`),
		RequiredSkills:      json.RawMessage(`["vuln-web-bac"]`),
		Reasoning:           "endpoint returns user PII",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID == 0 || got.CreatedAt.IsZero() {
		t.Fatalf("expected ID/CreatedAt populated, got %+v", got)
	}

	all, err := s.ListByEngagement(ctx, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("expected 1 row, got %d", len(all))
	}
	d := all[0]
	if d.FlowID != fid || d.Operation != "read" ||
		d.ResourceScope != ResourceScopePrivate || !d.CarriesAuth ||
		d.Reasoning != "endpoint returns user PII" {
		t.Fatalf("scalar mismatch: %+v", d)
	}
	if string(d.AttackSurfaces) != `["json", "query"]` &&
		string(d.AttackSurfaces) != `["json","query"]` {
		t.Fatalf("attack_surfaces: %s", d.AttackSurfaces)
	}
}

// TestStore_Append_DefaultsEmptyJSONArrays 空 jsonb 字段应自动落 '[]'，不触发 NOT NULL 错误。
func TestStore_Append_DefaultsEmptyJSONArrays(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)

	es := engagement.NewStore(pool)
	e, err := es.LookupOrCreate(ctx, "default", "h", engagement.ModeProxy)
	if err != nil {
		t.Fatal(err)
	}
	fs := flow.NewStore(pool, 0, 0)
	fid, err := fs.Append(ctx, flow.Flow{EngagementID: e.ID, Method: "GET", URL: "/", StatusCode: 200})
	if err != nil {
		t.Fatal(err)
	}

	s := NewStore(pool)
	if _, err := s.Append(ctx, Decision{
		EngagementID:  e.ID,
		FlowID:        fid,
		Operation:     "read",
		ResourceScope: ResourceScopeUnknown,
	}); err != nil {
		t.Fatalf("append with empty jsonb: %v", err)
	}
}
