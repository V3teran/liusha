//go:build integration

package traffic

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/V3teran/liusha/internal/dbtest"
	"github.com/V3teran/liusha/internal/task"
)

// newProxyTask 建一个代理捕获 task（proxy_traffic 消费方 / agent_traffic 归属方），返回 (pool, taskID)。
func newProxyTask(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()
	pool := dbtest.NewPgPool(t)
	tk, err := task.NewStore(pool).Create(context.Background(), task.NewParams{

		AssignmentID: dbtest.SeedAssignment(t, pool),
		Goal:         "test traffic ingest",
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
	pool, _ := newProxyTask(t)
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
	if got.ContentType != "application/json" || got.URL != "http://test.example.com/api/x" {
		t.Fatalf("meta fields mismatch: %+v", got)
	}
}

// TestProxyStore_ClaimThenListByTask 验证：ClaimUnconsumedByHost 把 host 的流量关联给某代理捕获
// task（M:N traffic_task），之后 ListByTask 按 captured_at 升序取回这批（含 raw）；GetByID 的
// ConsumedBy 列出该消费者。
func TestProxyStore_ClaimThenListByTask(t *testing.T) {
	ctx := context.Background()
	pool, taskID := newProxyTask(t)
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

	// GetByID 的 ConsumedBy 应含本 task；还没有对话时对话 ID 为空。
	got, err := s.GetByID(ctx, firstID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !consumedByTaskID(got.ConsumedBy, taskID) {
		t.Fatalf("ConsumedBy 应含 task %q, got %+v", taskID, got.ConsumedBy)
	}
	if got.ConsumedBy[0].ConversationID != "" {
		t.Fatalf("没有对话时对话 ID 应为空，得 %q", got.ConsumedBy[0].ConversationID)
	}

	// 消费任务连同它的对话 ID 一次查出（详情 chip 据此跳转），不逐个反查。
	var convID string
	if err := pool.QueryRow(ctx, `INSERT INTO conversation (title, task_id) VALUES ('test.example.com', $1) RETURNING id`,
		taskID).Scan(&convID); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetByID(ctx, firstID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ConsumedBy[0].ConversationID != convID {
		t.Fatalf("消费任务应带出对话 ID %q，得 %+v", convID, got.ConsumedBy)
	}
	// 跳转所需的来源与漏洞数一并带出
	if _, err := pool.Exec(ctx, `INSERT INTO finding (task_id, host, severity, summary) VALUES ($1, 'test.example.com', 'high', 'XSS')`, taskID); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetByID(ctx, firstID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if c := got.ConsumedBy[0]; c.FindingCount != 1 || c.Source == "" {
		t.Fatalf("消费任务应带出来源与漏洞数，得 %+v", c)
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

// TestProxyStore_ListByTaskFiltered_Pagination 验证：limit/offset 在 task 消费范围内起作用
// （captured_at DESC，最新优先）。
func TestProxyStore_ListByTaskFiltered_Pagination(t *testing.T) {
	ctx := context.Background()
	pool, taskID := newProxyTask(t)
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
	pool, taskID := newProxyTask(t)
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
	pool, taskID := newProxyTask(t)
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

// TestProxyStore_CountSince 验证：按捕获时间统计时间窗内的代理流量（概览的近期流量）。
func TestProxyStore_CountSince(t *testing.T) {
	ctx := context.Background()
	pool, _ := newProxyTask(t)
	s := NewProxyStore(pool)
	for range 3 {
		if _, err := s.Append(ctx, ProxyTraffic{Host: "h.example", Method: "GET", URL: "http://h.example/", Path: "/"}); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE proxy_traffic SET captured_at = now() - interval '2 hours'
		WHERE id = (SELECT min(id) FROM proxy_traffic)`); err != nil {
		t.Fatal(err)
	}
	n, err := s.CountSince(ctx, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Fatalf("近一小时应有 2 条，得 %d", n)
	}
}

// TestRetention_DeleteOlderThan 锁定两张流量表的留存批删语义：
//   - proxy_traffic：captured_at 过期的行连同 traffic_task 关联行一起删，新鲜行与关联行保留；
//   - agent_traffic：created_at 过期的行删除，新鲜行保留；
//   - 两者都循环批删到返回 0。
func TestRetention_DeleteOlderThan(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过集成测试")
	}
	ctx := context.Background()
	pool, taskID := newProxyTask(t)
	proxy := NewProxyStore(pool)
	agent := NewAgentStore(pool)

	reqRaw := []byte("GET / HTTP/1.1\r\nHost: old.example.com\r\n\r\n")
	oldID, err := proxy.Append(ctx, ProxyTraffic{
		Host: "old.example.com", Method: "GET", URL: "http://old.example.com/", Path: "/",
		StatusCode: 200, RequestRaw: reqRaw,
	})
	if err != nil {
		t.Fatalf("append old: %v", err)
	}
	freshID, err := proxy.Append(ctx, ProxyTraffic{
		Host: "fresh.example.com", Method: "GET", URL: "http://fresh.example.com/", Path: "/",
		StatusCode: 200, RequestRaw: reqRaw,
	})
	if err != nil {
		t.Fatalf("append fresh: %v", err)
	}
	// 过期捕获行回写 captured_at 并领取建关联
	base := time.Now().Add(-48 * time.Hour).Truncate(time.Second)
	if _, err = pool.Exec(ctx, "UPDATE proxy_traffic SET captured_at=$1 WHERE id=$2", base, oldID); err != nil {
		t.Fatalf("回写 captured_at: %v", err)
	}
	claimed, err := proxy.ClaimUnconsumedByHost(ctx, taskID, "old.example.com", 10)
	if err != nil || claimed != 1 {
		t.Fatalf("领取建关联: claimed=%d err=%v", claimed, err)
	}

	// agent 流量：过期一条 + 新鲜一条
	agentOld, err := agent.Append(ctx, AgentTraffic{
		TaskID: taskID, Tool: "curl", Method: "GET", URL: "http://old.example.com/a", Path: "/a", StatusCode: 200,
	})
	if err != nil {
		t.Fatalf("append agent old: %v", err)
	}
	if _, err = pool.Exec(ctx, "UPDATE agent_traffic SET created_at=$1 WHERE id=$2", base, agentOld); err != nil {
		t.Fatalf("回写 created_at: %v", err)
	}
	if _, err = agent.Append(ctx, AgentTraffic{
		TaskID: taskID, Tool: "curl", Method: "GET", URL: "http://fresh.example.com/a", Path: "/a", StatusCode: 200,
	}); err != nil {
		t.Fatalf("append agent fresh: %v", err)
	}

	cutoff := time.Now().Add(-24 * time.Hour)

	// 循环批删代理流量：过期行与关联行一起删
	var deleted int64
	for {
		n, err := proxy.DeleteOlderThan(ctx, cutoff)
		if err != nil {
			t.Fatal(err)
		}
		deleted += n
		if n == 0 {
			break
		}
	}
	if deleted != 1 {
		t.Fatalf("应删除 1 条捕获流量, got %d", deleted)
	}
	var joins int
	if err = pool.QueryRow(ctx, "SELECT count(*) FROM traffic_task WHERE traffic_id=$1", oldID).Scan(&joins); err != nil {
		t.Fatal(err)
	}
	if joins != 0 {
		t.Fatalf("关联行应随捕获行删除, got %d", joins)
	}
	var freshExists bool
	if err = pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM proxy_traffic WHERE id=$1)", freshID).Scan(&freshExists); err != nil {
		t.Fatal(err)
	}
	if !freshExists {
		t.Fatal("新鲜捕获行应保留")
	}
	var freshJoin int
	if err = pool.QueryRow(ctx, "SELECT count(*) FROM traffic_task WHERE traffic_id=$1", freshID).Scan(&freshJoin); err != nil {
		t.Fatal(err)
	}
	if freshJoin != 0 {
		t.Fatalf("新鲜捕获行未被领取过, joins=%d", freshJoin)
	}

	// agent 流量：过期一条
	deleted = 0
	for {
		n, err := agent.DeleteOlderThan(ctx, cutoff)
		if err != nil {
			t.Fatal(err)
		}
		deleted += n
		if n == 0 {
			break
		}
	}
	if deleted != 1 {
		t.Fatalf("agent 流量应删除 1 条, got %d", deleted)
	}
}
