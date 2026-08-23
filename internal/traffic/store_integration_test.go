//go:build integration

package traffic

import (
	"bytes"
	"context"
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
		ScenarioID:   "api-pentest",
		AssignmentID: dbtest.SeedAssignment(t, pool, "api-pentest"),
		Brief:        "test traffic ingest",
		TargetHost:   "test.example.com",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	return pool, tk.ID
}

// -------- ProxyStore（代理捕获流量，按 host 归属） --------

// TestProxyStore_Append_RawRoundtrip 验证：raw 报文（请求/响应）作为唯一存储原样写入并取回，
// body 截断在 ingest 组装 raw 前完成，store 不再二次截断。
func TestProxyStore_Append_RawRoundtrip(t *testing.T) {
	ctx := context.Background()
	pool, _ := newPassiveTask(t)
	s := NewProxyStore(pool)

	reqRaw := []byte("POST /api/x HTTP/1.1\r\nHost: test.example.com\r\nContent-Type: application/json\r\n\r\n{\"a\":1}")
	respRaw := []byte("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n{\"ok\":true}")
	id, err := s.Append(ctx, ProxyTraffic{
		Host:        "test.example.com",
		Method:      "POST",
		URL:         "http://test.example.com/api/x",
		Path:        "/api/x",
		StatusCode:  200,
		RequestRaw:  reqRaw,
		ResponseRaw: respRaw,
		ContentType: "application/json",
		HTTPVersion: "HTTP/1.1",
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
	if !bytes.Equal(got.RequestRaw, reqRaw) || !bytes.Equal(got.ResponseRaw, respRaw) {
		t.Fatalf("raw roundtrip mismatch: req=%q resp=%q", got.RequestRaw, got.ResponseRaw)
	}
	if got.Method != "POST" || got.Path != "/api/x" || got.StatusCode != 200 {
		t.Fatalf("scalar fields mismatch: %+v", got)
	}
	if got.ContentType != "application/json" || got.HTTPVersion != "HTTP/1.1" {
		t.Fatalf("meta fields mismatch: %+v", got)
	}
}

// TestProxyStore_ClaimThenListByTask 验证：ClaimUnconsumedByHost 把 host 的流量关联给某 passive
// task（M:N traffic_task），之后 ListByTask 按 captured_at 升序取回这批（含 raw）；GetByID 的
// ConsumedBy 列出该消费者。取代旧 consumed_by_task_id 单列模型。
func TestProxyStore_ClaimThenListByTask(t *testing.T) {
	ctx := context.Background()
	pool, taskID := newPassiveTask(t)
	s := NewProxyStore(pool)

	var firstID int64
	paths := []string{"/a", "/b", "/c"}
	for i, p := range paths {
		id, err := s.Append(ctx, ProxyTraffic{
			Host: "test.example.com", Method: "GET",
			URL: "http://test.example.com" + p, Path: p, StatusCode: 200,
			RequestRaw: []byte("GET " + p + " HTTP/1.1\r\nHost: test.example.com\r\n\r\n"),
		})
		if err != nil {
			t.Fatalf("seed %s: %v", p, err)
		}
		if i == 0 {
			firstID = id
		}
	}

	claimed, err := s.ClaimUnconsumedByHost(ctx, taskID, "test.example.com", 100)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if claimed != 3 {
		t.Fatalf("应领取 3 条，得 %d", claimed)
	}

	// 重复领取幂等：本 task 已关联全部，再领得 0。
	again, err := s.ClaimUnconsumedByHost(ctx, taskID, "test.example.com", 100)
	if err != nil {
		t.Fatalf("re-claim: %v", err)
	}
	if again != 0 {
		t.Fatalf("重复领取应幂等得 0，得 %d", again)
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

	// GetByID 的 ConsumedBy 应含本 task。
	got, err := s.GetByID(ctx, firstID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !consumedByTaskID(got.ConsumedBy, taskID) {
		t.Fatalf("ConsumedBy 应含 task %q, got %+v", taskID, got.ConsumedBy)
	}
}

// consumedByTaskID 测试辅助：判断消费者列表是否含某 task。
func consumedByTaskID(consumers []ConsumerTask, taskID string) bool {
	for _, c := range consumers {
		if c.TaskID == taskID {
			return true
		}
	}
	return false
}

// TestProxyStore_ClaimByIDs 验证：显式 id 集合领取（manual TrafficIDs 路径）只关联点名的流量。
func TestProxyStore_ClaimByIDs(t *testing.T) {
	ctx := context.Background()
	pool, taskID := newPassiveTask(t)
	s := NewProxyStore(pool)

	var ids []int64
	for _, p := range []string{"/x", "/y", "/z"} {
		id, err := s.Append(ctx, ProxyTraffic{
			Host: "test.example.com", Method: "GET",
			URL: "http://test.example.com" + p, Path: p, StatusCode: 200,
		})
		if err != nil {
			t.Fatalf("seed %s: %v", p, err)
		}
		ids = append(ids, id)
	}

	// 只领前两条。
	claimed, err := s.ClaimByIDs(ctx, taskID, ids[:2])
	if err != nil {
		t.Fatalf("claim by ids: %v", err)
	}
	if claimed != 2 {
		t.Fatalf("应领取 2 条，得 %d", claimed)
	}
	list, err := s.ListByTask(ctx, taskID)
	if err != nil {
		t.Fatalf("list by task: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expect 2 rows, got %d", len(list))
	}
	// 重复领取幂等。
	again, err := s.ClaimByIDs(ctx, taskID, ids[:2])
	if err != nil {
		t.Fatalf("re-claim by ids: %v", err)
	}
	if again != 0 {
		t.Fatalf("重复 ClaimByIDs 应幂等得 0，得 %d", again)
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
