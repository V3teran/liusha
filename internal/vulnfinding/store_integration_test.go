//go:build integration

package vulnfinding

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

	f, isFirst, err := s.Save(ctx, VulnFinding{
		EngagementID: eid,
		Host:         "h",
		Kind:         "bac.horizontal_priv_esc",
		Severity:     SeverityHigh,
		Title:        "GET /api/order/:oid",
		Target:       json.RawMessage(`{"url":"/api/order/7"}`),
		Evidence:     json.RawMessage(`{"violating":["test"]}`),
		DedupKey:     "bac.horizontal_priv_esc:vulnapp:GET:/api/order/:oid",
	})
	if err != nil {
		t.Fatalf("save 新 finding 失败: %v", err)
	}
	if !isFirst {
		t.Fatalf("第一次写入应 isFirstSeen=true")
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
}

// TestStore_Save_AppendOnly_Rediscovery 验证 v1.2 append-only 语义：
// 同 (host, dedup_key) 第二次 Save 不再 UPSERT 合并 evidence，而是插入新行；
// 第一次 isFirstSeen=true，第二次 false；ListByEngagement 走 DISTINCT ON dedup 视图仍返 1 行。
func TestStore_Save_AppendOnly_Rediscovery(t *testing.T) {
	ctx := context.Background()
	s, eid := setup(t)
	dk := "bac.horizontal_priv_esc:vulnapp:GET:/api/order/:oid"

	a, isFirstA, err := s.Save(ctx, VulnFinding{
		EngagementID: eid,
		Host:         "h",
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
	if !isFirstA {
		t.Fatalf("第一次 save 应 isFirstSeen=true")
	}

	time.Sleep(10 * time.Millisecond)

	b, isFirstB, err := s.Save(ctx, VulnFinding{
		EngagementID: eid,
		Host:         "h",
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
	if isFirstB {
		t.Fatalf("第二次 save 应 isFirstSeen=false (append-only 重发现)")
	}
	if a.ID == b.ID {
		t.Fatalf("append-only 应插入新行，ID 必须不同: a=%s b=%s", a.ID, b.ID)
	}

	// b 是新行，evidence 不应继承 a 的字段（不再 merge）
	var ev map[string]any
	if err := json.Unmarshal(b.Evidence, &ev); err != nil {
		t.Fatalf("evidence 不是合法 json: %v", err)
	}
	if _, ok := ev["first"]; ok {
		t.Fatalf("append-only 不应合并 a 的字段，但 b.evidence 含 first: %+v", ev)
	}
	if v, _ := ev["second"].(bool); !v {
		t.Fatalf("b.evidence.second 应存在: %+v", ev)
	}

	// ListByEngagement 走 DISTINCT ON dedup 视图（取最新一行）
	all, err := s.ListByEngagement(ctx, eid)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("DISTINCT ON dedup 后应仅 1 行, got %d", len(all))
	}
	if all[0].ID != b.ID {
		t.Fatalf("应返回最新行 (b.ID=%s), got %s", b.ID, all[0].ID)
	}
}

// TestStore_OnSavedHook 验证：注册 hook 后，Save 异步触发 hook（chan + timeout 验证）。
func TestStore_OnSavedHook(t *testing.T) {
	ctx := context.Background()
	s, eid := setup(t)

	got := make(chan VulnFinding, 4)
	s.OnSaved(func(_ context.Context, hookEID string, f VulnFinding) {
		if hookEID != eid {
			t.Errorf("hook eid 不匹配: %s vs %s", hookEID, eid)
		}
		got <- f
	})

	saved, _, err := s.Save(ctx, VulnFinding{
		EngagementID: eid,
		Host:         "h",
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

	if _, _, err := s.Save(ctx, VulnFinding{
		EngagementID: eid,
		Host:         "h",
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
	s.OnSaved(func(_ context.Context, _ string, _ VulnFinding) { c1 <- struct{}{} })
	s.OnSaved(func(_ context.Context, _ string, _ VulnFinding) { c2 <- struct{}{} })

	if _, _, err := s.Save(ctx, VulnFinding{
		EngagementID: eid, Host: "h", Kind: "k", Severity: SeverityLow, Title: "t", DedupKey: "multi:1",
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
