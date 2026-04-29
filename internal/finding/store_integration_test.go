//go:build integration

package finding

import (
	"context"
	"encoding/json"
	"testing"
	"time"

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

// TestStore_Save_NewFinding 验证：新 dedup_key 直接插入，并能通过 GetByID 读回。
func TestStore_Save_NewFinding(t *testing.T) {
	ctx := context.Background()
	s, eid := setup(t)

	f, err := s.Save(ctx, Finding{
		EngagementID: eid,
		Kind:         "bac.horizontal_priv_esc",
		Severity:     SeverityHigh,
		Title:        "GET /api/order/:oid",
		Target:       json.RawMessage(`{"url":"/api/order/7"}`),
		Evidence:     json.RawMessage(`{"violating":["test"]}`),
		Tool:         "bac_probe",
		DedupKey:     "bac.horizontal_priv_esc:vulnapp:GET:/api/order/:oid",
	})
	if err != nil {
		t.Fatalf("save 新 finding 失败: %v", err)
	}
	if f.ID == "" {
		t.Fatalf("save 后 ID 应非空")
	}
	if f.Severity != SeverityHigh {
		t.Fatalf("severity 应为 high, got %s", f.Severity)
	}
	if f.Confidence != ConfidenceUnverified {
		t.Fatalf("confidence 缺省应为 unverified, got %s", f.Confidence)
	}

	got, err := s.GetByID(ctx, f.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.ID != f.ID {
		t.Fatalf("GetByID id 不匹配: %s vs %s", got.ID, f.ID)
	}
	if got.Tool != "bac_probe" {
		t.Fatalf("Tool 字段未保存: %+v", got)
	}
}

// TestStore_Save_OnConflictMergesEvidence 验证：同 dedup_key 第二次 Save，
// evidence 被 jsonb || 合并（旧字段保留 + 新字段加入），返回的 Finding 是合并后的；
// updated_at 推进；ListByEngagement 仍只有 1 行。
func TestStore_Save_OnConflictMergesEvidence(t *testing.T) {
	ctx := context.Background()
	s, eid := setup(t)
	dk := "bac.horizontal_priv_esc:vulnapp:GET:/api/order/:oid"

	a, err := s.Save(ctx, Finding{
		EngagementID: eid,
		Kind:         "bac.horizontal_priv_esc",
		Severity:     SeverityHigh,
		Title:        "GET /api/order/:oid",
		Target:       json.RawMessage(`{"url":"/api/order/7"}`),
		Evidence:     json.RawMessage(`{"violating":["test"],"first":true}`),
		DedupKey:     dk,
	})
	if err != nil {
		t.Fatalf("first save: %v", err)
	}

	// 让 updated_at 有足够分辨率推进。
	time.Sleep(10 * time.Millisecond)

	b, err := s.Save(ctx, Finding{
		EngagementID: eid,
		Kind:         "bac.horizontal_priv_esc",
		Severity:     SeverityHigh,
		Title:        "GET /api/order/:oid",
		Target:       json.RawMessage(`{"url":"/api/order/9"}`),
		Evidence:     json.RawMessage(`{"violating":["m233241"],"second":true}`),
		DedupKey:     dk,
	})
	if err != nil {
		t.Fatalf("second save: %v", err)
	}
	if a.ID != b.ID {
		t.Fatalf("UNIQUE 失效，应返回同一 ID, got %s vs %s", a.ID, b.ID)
	}
	if b.UpdatedAt.Before(a.UpdatedAt) {
		t.Fatalf("updated_at 不应倒退: %v vs %v", a.UpdatedAt, b.UpdatedAt)
	}

	var ev map[string]any
	if err := json.Unmarshal(b.Evidence, &ev); err != nil {
		t.Fatalf("evidence 不是合法 json: %v", err)
	}
	// 旧字段 first 必须保留（jsonb || 顶层合并语义）。
	if v, _ := ev["first"].(bool); !v {
		t.Fatalf("evidence.first 应保留: %+v", ev)
	}
	// 新字段 second 必须并入。
	if v, _ := ev["second"].(bool); !v {
		t.Fatalf("evidence.second 应并入: %+v", ev)
	}
	// violating 在 jsonb 顶层 || 下会被新值覆盖，仅断言 key 存在。
	if _, ok := ev["violating"]; !ok {
		t.Fatalf("evidence.violating 缺失: %+v", ev)
	}

	all, err := s.ListByEngagement(ctx, eid)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("dedup 后应仅有 1 行, got %d", len(all))
	}
}

// TestStore_OnSavedHook 验证：注册 hook 后，Save 异步触发 hook（chan + timeout 验证）。
func TestStore_OnSavedHook(t *testing.T) {
	ctx := context.Background()
	s, eid := setup(t)

	got := make(chan Finding, 4)
	s.OnSaved(func(_ context.Context, hookEID string, f Finding) {
		if hookEID != eid {
			t.Errorf("hook eid 不匹配: %s vs %s", hookEID, eid)
		}
		got <- f
	})

	saved, err := s.Save(ctx, Finding{
		EngagementID: eid,
		Kind:         "demo.kind",
		Severity:     SeverityMedium,
		Title:        "demo",
		DedupKey:     "demo:1",
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	select {
	case f := <-got:
		if f.ID != saved.ID {
			t.Fatalf("hook 收到的 finding id 不匹配: %s vs %s", f.ID, saved.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("hook 未在 2s 内触发（应异步触发 1 次）")
	}

	// 确保只触发了 1 次（短 backoff 后再读应阻塞超时）。
	select {
	case extra := <-got:
		t.Fatalf("hook 触发次数 > 1, 多余: %+v", extra)
	case <-time.After(200 * time.Millisecond):
	}
}

// TestStore_HasDedupKey 验证：Save 后 HasDedupKey 返 true；
// 查不存在的 key 返 false；写入前同 key 也是 false。
func TestStore_HasDedupKey(t *testing.T) {
	ctx := context.Background()
	s, eid := setup(t)
	dk := "bac.horizontal_priv_esc:vulnapp:GET:/api/has/dedup"

	exists, err := s.HasDedupKey(ctx, eid, dk)
	if err != nil {
		t.Fatalf("HasDedupKey 前置查询失败: %v", err)
	}
	if exists {
		t.Fatalf("写入前不应存在: dk=%s", dk)
	}

	if _, err := s.Save(ctx, Finding{
		EngagementID: eid,
		Kind:         "bac.horizontal_priv_esc",
		Severity:     SeverityHigh,
		Title:        "GET /api/has/dedup",
		DedupKey:     dk,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	exists, err = s.HasDedupKey(ctx, eid, dk)
	if err != nil {
		t.Fatalf("HasDedupKey 后置查询失败: %v", err)
	}
	if !exists {
		t.Fatalf("写入后应存在: dk=%s", dk)
	}

	exists, err = s.HasDedupKey(ctx, eid, "bac.no_such_key:nope")
	if err != nil {
		t.Fatalf("HasDedupKey 不存在键查询失败: %v", err)
	}
	if exists {
		t.Fatal("不存在的 dedup_key 不应返回 true")
	}
}

// TestStore_OnSavedHook_MultipleSubscribers 验证：注册多个 hook 都能被异步触发。
func TestStore_OnSavedHook_MultipleSubscribers(t *testing.T) {
	ctx := context.Background()
	s, eid := setup(t)

	c1 := make(chan struct{}, 1)
	c2 := make(chan struct{}, 1)
	s.OnSaved(func(_ context.Context, _ string, _ Finding) { c1 <- struct{}{} })
	s.OnSaved(func(_ context.Context, _ string, _ Finding) { c2 <- struct{}{} })

	if _, err := s.Save(ctx, Finding{
		EngagementID: eid, Kind: "k", Severity: SeverityLow, Title: "t", DedupKey: "multi:1",
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	for i, c := range []chan struct{}{c1, c2} {
		select {
		case <-c:
		case <-time.After(2 * time.Second):
			t.Fatalf("hook #%d 未触发", i+1)
		}
	}
}
