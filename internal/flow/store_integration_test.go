//go:build integration

package flow

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/V3teran/liusha/internal/dbtest"
	"github.com/V3teran/liusha/internal/task"
	"github.com/jackc/pgx/v5/pgxpool"
)

// newPassiveTask 建一个 passive task（proxy_traffic 消费方 / agent_traffic 归属方），返回 (pool, taskID)。
func newPassiveTask(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()
	pool := dbtest.NewPgPool(t)
	tk, err := task.NewStore(pool).Create(context.Background(), task.NewParams{
		Mode:         task.ModePassive,
		AssignmentID: dbtest.SeedAssignment(t, pool, "passive"),
		TargetHost:   "test.example.com",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	return pool, tk.ID
}

// -------- ProxyStore（代理捕获流量，按 host 归属） --------

// TestProxyStore_Append_TruncatesLargeBody 验证：超过 32 KiB 的 body 被截断到精确 32 KiB。
// 旧测试用可配置 maxReqBody=1024/maxRespBody=2048，新 store 固定 defaultMaxBody（32 KiB），
// 故这里用大于阈值的 body 验证截断。
func TestProxyStore_Append_TruncatesLargeBody(t *testing.T) {
	ctx := context.Background()
	pool, _ := newPassiveTask(t)
	s := NewProxyStore(pool)

	big := bytes.Repeat([]byte("x"), defaultMaxBody+5000)
	id, err := s.Append(ctx, ProxyTraffic{
		Host:            "test.example.com",
		Method:          "POST",
		URL:             "http://test.example.com/api/x",
		Path:            "/api/x",
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
	if len(got.RequestBody) != defaultMaxBody {
		t.Fatalf("req body len: want %d (truncated), got %d", defaultMaxBody, len(got.RequestBody))
	}
	if len(got.ResponseBody) != defaultMaxBody {
		t.Fatalf("resp body len: want %d (truncated), got %d", defaultMaxBody, len(got.ResponseBody))
	}
	if !bytes.Equal(got.RequestBody, big[:defaultMaxBody]) {
		t.Fatalf("req body bytes mismatch")
	}
	if got.Method != "POST" || got.Path != "/api/x" || got.StatusCode != 200 {
		t.Fatalf("scalar fields mismatch: %+v", got)
	}
}

// TestProxyStore_Append_SmallBodyNoTruncation 验证：未达阈值的 body 原样保留。
func TestProxyStore_Append_SmallBodyNoTruncation(t *testing.T) {
	ctx := context.Background()
	pool, _ := newPassiveTask(t)
	s := NewProxyStore(pool)

	small := []byte("hello")
	id, err := s.Append(ctx, ProxyTraffic{
		Host:        "test.example.com",
		Method:      "GET",
		URL:         "http://test.example.com/health",
		Path:        "/health",
		RequestBody: small,
		StatusCode:  204,
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	got, err := s.GetByID(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !bytes.Equal(got.RequestBody, small) {
		t.Fatalf("req body roundtrip mismatch: %q", got.RequestBody)
	}
}

// TestProxyStore_ClaimThenListByTask 验证：ClaimUnconsumedByHost 把 host 的未消费流量
// 关联给某 passive task，之后 ListByTask 按 captured_at 升序取回这批（含 body）。
// 取代旧「双表合并 ListByOwner」——现按 host claim → task 消费模型。
func TestProxyStore_ClaimThenListByTask(t *testing.T) {
	ctx := context.Background()
	pool, taskID := newPassiveTask(t)
	s := NewProxyStore(pool)

	paths := []string{"/a", "/b", "/c"}
	for _, p := range paths {
		if _, err := s.Append(ctx, ProxyTraffic{
			Host: "test.example.com", Method: "GET",
			URL: "http://test.example.com" + p, Path: p, StatusCode: 200,
		}); err != nil {
			t.Fatalf("seed %s: %v", p, err)
		}
	}

	claimed, err := s.ClaimUnconsumedByHost(ctx, taskID, "test.example.com", 100)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if claimed != 3 {
		t.Fatalf("应领取 3 条，得 %d", claimed)
	}

	list, err := s.ListByTask(ctx, taskID)
	if err != nil {
		t.Fatalf("list by task: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("expect 3 rows, got %d", len(list))
	}
	// captured_at ASC → 与插入顺序一致
	if list[0].Path != "/a" || list[2].Path != "/c" {
		t.Fatalf("排序应按 captured_at 升序: %+v", list)
	}
	for _, f := range list {
		if f.ConsumedByTaskID != taskID {
			t.Fatalf("consumed_by_task_id 应为 %q, got %q", taskID, f.ConsumedByTaskID)
		}
	}
}

// TestProxyStore_ListByTaskFiltered_Pagination 验证：limit/offset 在 task 消费范围内起作用
// （captured_at DESC，最新优先）。取代旧 ListByOwner 分页用例。
func TestProxyStore_ListByTaskFiltered_Pagination(t *testing.T) {
	ctx := context.Background()
	pool, taskID := newPassiveTask(t)
	s := NewProxyStore(pool)

	paths := []string{"/p1", "/p2", "/p3", "/p4", "/p5"}
	for _, p := range paths {
		if _, err := s.Append(ctx, ProxyTraffic{
			Host: "test.example.com", Method: "GET",
			URL: "http://test.example.com" + p, Path: p, StatusCode: 200,
		}); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	if _, err := s.ClaimUnconsumedByHost(ctx, taskID, "test.example.com", 100); err != nil {
		t.Fatalf("claim: %v", err)
	}

	// captured_at DESC：最新的 /p5 在前。
	page1, err := s.ListByTaskFiltered(ctx, taskID, ProxyListFilter{Limit: 2, Offset: 0})
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(page1) != 2 || page1[0].Path != "/p5" || page1[1].Path != "/p4" {
		t.Fatalf("page1 unexpected: %+v", page1)
	}
	page2, err := s.ListByTaskFiltered(ctx, taskID, ProxyListFilter{Limit: 2, Offset: 2})
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(page2) != 2 || page2[0].Path != "/p3" || page2[1].Path != "/p2" {
		t.Fatalf("page2 unexpected: %+v", page2)
	}
}

// -------- AgentStore（agent 自产流量，按 task 归属） --------

// TestAgentStore_Append_TruncatesLargeBody 验证：agent_traffic 大 body 同样截断到 32 KiB。
func TestAgentStore_Append_TruncatesLargeBody(t *testing.T) {
	ctx := context.Background()
	pool, taskID := newPassiveTask(t)
	s := NewAgentStore(pool)

	big := bytes.Repeat([]byte("y"), defaultMaxBody+3000)
	id, err := s.Append(ctx, AgentTraffic{
		TaskID:       taskID,
		Tool:         "curl",
		Method:       "POST",
		URL:          "http://test.example.com/c",
		Path:         "/c",
		StatusCode:   500,
		RequestBody:  big,
		ResponseBody: big,
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	got, err := s.GetByID(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got.RequestBody) != defaultMaxBody || len(got.ResponseBody) != defaultMaxBody {
		t.Fatalf("body len want %d/%d, got req=%d resp=%d",
			defaultMaxBody, defaultMaxBody, len(got.RequestBody), len(got.ResponseBody))
	}
	if got.TaskID != taskID || got.Tool != "curl" || got.StatusCode != 500 {
		t.Fatalf("scalar fields mismatch: %+v", got)
	}
}

// TestAgentStore_ListByTaskFiltered 验证：Append 多条后 ListByTaskFiltered 按 task 取回摘要，
// created_at DESC（最新优先），并可按 host 过滤。
func TestAgentStore_ListByTaskFiltered(t *testing.T) {
	ctx := context.Background()
	pool, taskID := newPassiveTask(t)
	s := NewAgentStore(pool)

	seed := []struct {
		host, path string
		status     int
	}{
		{"test.example.com", "/a", 200},
		{"test.example.com", "/b", 404},
		{"other.example.com", "/c", 500},
	}
	for _, r := range seed {
		if _, err := s.Append(ctx, AgentTraffic{
			TaskID: taskID, Tool: "curl", Method: "GET",
			URL: "http://" + r.host + r.path, Host: r.host, Path: r.path, StatusCode: r.status,
		}); err != nil {
			t.Fatalf("seed %s%s: %v", r.host, r.path, err)
		}
	}

	all, err := s.ListByTaskFiltered(ctx, taskID, AgentListFilter{Limit: 100})
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expect 3 rows, got %d", len(all))
	}
	// created_at DESC：最后插入的 /c 在前。
	if all[0].Path != "/c" {
		t.Fatalf("排序应按 created_at 降序，头部应为 /c, got %+v", all)
	}

	// host 过滤：仅 test.example.com 两条。
	filtered, err := s.ListByTaskFiltered(ctx, taskID, AgentListFilter{Host: "test.example.com", Limit: 100})
	if err != nil {
		t.Fatalf("list filtered: %v", err)
	}
	if len(filtered) != 2 {
		t.Fatalf("host 过滤应得 2 条，got %d", len(filtered))
	}
	for _, sum := range filtered {
		if sum.Host != "test.example.com" {
			t.Fatalf("过滤后混入了 host=%q", sum.Host)
		}
	}
}
