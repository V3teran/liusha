//go:build integration

package flow

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/V3teran/liusha/internal/dbtest"
	"github.com/V3teran/liusha/internal/engagement"
)

// setup 启动 Postgres 容器、懒建 engagement，返回 (Store, engagementID)。
// maxReqBody=1024 / maxRespBody=2048 用于覆盖截断测试。
func setup(t *testing.T) (*Store, string) {
	t.Helper()
	pool := dbtest.NewPgPool(t)
	es := engagement.NewStore(pool)
	e, err := es.LookupOrCreate(context.Background(), "default", "h", engagement.ModeProxy)
	if err != nil {
		t.Fatalf("lookup engagement: %v", err)
	}
	return NewStore(pool, 1024, 2048), e.ID
}

// TestStore_Append_TruncatesLargeBody 验证：超过 max 的 body 被截断到精确 max 字节，
// truncated 标志位置 true，bytea 字段往返一致。
func TestStore_Append_TruncatesLargeBody(t *testing.T) {
	ctx := context.Background()
	s, eid := setup(t)

	big := bytes.Repeat([]byte("x"), 5000)
	id, err := s.Append(ctx, Flow{
		EngagementID:    eid,
		Method:          "POST",
		URL:             "/api/x",
		RequestHeaders:  json.RawMessage(`{"x":"1"}`),
		RequestBody:     big,
		StatusCode:      200,
		ResponseHeaders: json.RawMessage(`{"y":"2"}`),
		ResponseBody:    big,
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if id <= 0 {
		t.Fatalf("expect id > 0, got %d", id)
	}

	got, err := s.GetByID(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.RequestTruncated || !got.ResponseTruncated {
		t.Fatalf("expect both truncated=true, got req=%v resp=%v", got.RequestTruncated, got.ResponseTruncated)
	}
	if len(got.RequestBody) != 1024 {
		t.Fatalf("req body len: want 1024, got %d", len(got.RequestBody))
	}
	if len(got.ResponseBody) != 2048 {
		t.Fatalf("resp body len: want 2048, got %d", len(got.ResponseBody))
	}
	if !bytes.Equal(got.RequestBody, big[:1024]) {
		t.Fatalf("req body bytes mismatch")
	}
	if !bytes.Equal(got.ResponseBody, big[:2048]) {
		t.Fatalf("resp body bytes mismatch")
	}
	if got.Method != "POST" || got.URL != "/api/x" || got.StatusCode != 200 {
		t.Fatalf("scalar fields mismatch: %+v", got)
	}
}

// TestStore_Append_SmallBodyNoTruncation 验证：未达 max 的 body 原样保留，标志位 false。
func TestStore_Append_SmallBodyNoTruncation(t *testing.T) {
	ctx := context.Background()
	s, eid := setup(t)

	small := []byte("hello")
	id, err := s.Append(ctx, Flow{
		EngagementID: eid,
		Method:       "GET",
		URL:          "/health",
		RequestBody:  small,
		StatusCode:   204,
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	got, err := s.GetByID(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.RequestTruncated || got.ResponseTruncated {
		t.Fatalf("expect both truncated=false, got req=%v resp=%v", got.RequestTruncated, got.ResponseTruncated)
	}
	if !bytes.Equal(got.RequestBody, small) {
		t.Fatalf("req body roundtrip mismatch: %q", got.RequestBody)
	}
}

// TestStore_AppendBatch_CopyFrom 验证：CopyFrom 批插 N 条，所有行可被 ListByEngagement 检出，
// 且批内大 body 同样按 max 截断。
func TestStore_AppendBatch_CopyFrom(t *testing.T) {
	ctx := context.Background()
	s, eid := setup(t)

	big := bytes.Repeat([]byte("y"), 3000)
	flows := []Flow{
		{EngagementID: eid, Method: "GET", URL: "/a", StatusCode: 200, RequestBody: []byte("a")},
		{EngagementID: eid, Method: "GET", URL: "/b", StatusCode: 404, ResponseBody: big},
		{EngagementID: eid, Method: "POST", URL: "/c", StatusCode: 500, RequestBody: big, ResponseBody: big},
	}
	if err := s.AppendBatch(ctx, flows); err != nil {
		t.Fatalf("append batch: %v", err)
	}

	list, err := s.ListByEngagement(ctx, eid, 100, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("expect 3 rows, got %d", len(list))
	}

	// /a 两侧都小；/b response 大；/c 两侧都大
	wantTrunc := map[string][2]bool{
		"/a": {false, false},
		"/b": {false, true},
		"/c": {true, true},
	}
	for _, sum := range list {
		want, ok := wantTrunc[sum.URL]
		if !ok {
			t.Fatalf("unexpected url %q", sum.URL)
		}
		if sum.RequestTruncated != want[0] || sum.ResponseTruncated != want[1] {
			t.Fatalf("url=%s trunc want %v, got req=%v resp=%v", sum.URL, want, sum.RequestTruncated, sum.ResponseTruncated)
		}
		if sum.ID <= 0 {
			t.Fatalf("expect id > 0 for %s", sum.URL)
		}
	}
}

// TestStore_AppendBatch_Empty 验证：空切片不报错。
func TestStore_AppendBatch_Empty(t *testing.T) {
	ctx := context.Background()
	s, _ := setup(t)
	if err := s.AppendBatch(ctx, nil); err != nil {
		t.Fatalf("nil slice: %v", err)
	}
	if err := s.AppendBatch(ctx, []Flow{}); err != nil {
		t.Fatalf("empty slice: %v", err)
	}
}

// TestStore_ListByEngagement_Pagination 验证：limit / offset 起作用，按 ts 升序。
func TestStore_ListByEngagement_Pagination(t *testing.T) {
	ctx := context.Background()
	s, eid := setup(t)

	urls := []string{"/p1", "/p2", "/p3", "/p4", "/p5"}
	for _, u := range urls {
		if _, err := s.Append(ctx, Flow{EngagementID: eid, Method: "GET", URL: u, StatusCode: 200}); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	page1, err := s.ListByEngagement(ctx, eid, 2, 0)
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(page1) != 2 || page1[0].URL != "/p1" || page1[1].URL != "/p2" {
		t.Fatalf("page1 unexpected: %+v", page1)
	}
	page2, err := s.ListByEngagement(ctx, eid, 2, 2)
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(page2) != 2 || page2[0].URL != "/p3" || page2[1].URL != "/p4" {
		t.Fatalf("page2 unexpected: %+v", page2)
	}
}
