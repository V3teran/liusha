package notes

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// newTestStore 启 miniredis + go-redis client，返回 *RedisStore 与 miniredis 实例。
func newTestStore(t *testing.T, cfg Config) (*RedisStore, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return NewRedis(client, cfg), mr
}

// fakeCompactor 是测试用 Compactor 桩。
type fakeCompactor struct {
	calls  int32
	out    []byte // 返回的 summary entry；nil 时返回默认值
	err    error
	delay  time.Duration // 模拟慢调用，用于并发锁测试
	lastIn []json.RawMessage
	lastMu sync.Mutex
}

func (f *fakeCompactor) Compact(ctx context.Context, olds []json.RawMessage) ([]byte, error) {
	atomic.AddInt32(&f.calls, 1)
	f.lastMu.Lock()
	f.lastIn = olds
	f.lastMu.Unlock()
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if f.err != nil {
		return nil, f.err
	}
	if f.out != nil {
		return f.out, nil
	}
	return []byte(`{"content":"[summary]","agent_run_id":"compactor"}`), nil
}

// TestAppendThenRead 写 3 条 → 读回 3 条按写入顺序、内容一致。
func TestAppendThenRead(t *testing.T) {
	s, _ := newTestStore(t, Config{KeyPrefix: "test:note:"})
	ctx := context.Background()

	entries := [][]byte{
		[]byte(`{"content":"a","agent_run_id":"t1"}`),
		[]byte(`{"content":"b","agent_run_id":"t1"}`),
		[]byte(`{"content":"c","agent_run_id":"t2"}`),
	}
	for _, e := range entries {
		if err := s.AppendNote(ctx, "eng-1", "h1", e); err != nil {
			t.Fatalf("AppendNote: %v", err)
		}
	}

	got, err := s.ReadNotes(ctx, "eng-1", "h1")
	if err != nil {
		t.Fatalf("ReadNotes: %v", err)
	}
	var parsed struct {
		Notes []json.RawMessage `json:"notes"`
	}
	if err := json.Unmarshal(got, &parsed); err != nil {
		t.Fatalf("unmarshal: %v\nraw=%s", err, got)
	}
	if len(parsed.Notes) != 3 {
		t.Fatalf("want 3 notes, got %d: %s", len(parsed.Notes), got)
	}
	for i, e := range entries {
		if string(parsed.Notes[i]) != string(e) {
			t.Fatalf("notes[%d]: want %s, got %s", i, e, parsed.Notes[i])
		}
	}
}

// TestHostIsolation 同 engagement 不同 host 互不干扰。v0033 关键不变量。
func TestHostIsolation(t *testing.T) {
	s, _ := newTestStore(t, Config{KeyPrefix: "test:note:"})
	ctx := context.Background()

	_ = s.AppendNote(ctx, "eng-iso", "hostA", []byte(`{"content":"a-only"}`))
	_ = s.AppendNote(ctx, "eng-iso", "hostB", []byte(`{"content":"b-only"}`))
	_ = s.AppendNote(ctx, "eng-iso", "hostA", []byte(`{"content":"a-again"}`))

	gotA, _ := s.ReadNotes(ctx, "eng-iso", "hostA")
	gotB, _ := s.ReadNotes(ctx, "eng-iso", "hostB")

	var pa, pb struct {
		Notes []json.RawMessage `json:"notes"`
	}
	_ = json.Unmarshal(gotA, &pa)
	_ = json.Unmarshal(gotB, &pb)

	if len(pa.Notes) != 2 {
		t.Fatalf("hostA 期望 2 条，实际 %d: %s", len(pa.Notes), gotA)
	}
	if len(pb.Notes) != 1 {
		t.Fatalf("hostB 期望 1 条，实际 %d: %s", len(pb.Notes), gotB)
	}
	if string(pb.Notes[0]) != `{"content":"b-only"}` {
		t.Fatalf("hostB 不应混入 hostA 内容，实际 %s", pb.Notes[0])
	}
}

// TestFallbackTrimWithoutCompactor 未注入 Compactor 时超阈值直接 LTRIM 兜底。
// 防 key 无限增长——安全网行为。
func TestFallbackTrimWithoutCompactor(t *testing.T) {
	s, _ := newTestStore(t, Config{
		KeyPrefix:        "test:note:",
		MaxEntries:       5,
		CompactThreshold: 5,
	})
	ctx := context.Background()

	for i := 0; i < 10; i++ {
		e := []byte(fmt.Sprintf(`{"content":"n%d","agent_run_id":"t"}`, i))
		if err := s.AppendNote(ctx, "eng-2", "h1", e); err != nil {
			t.Fatalf("AppendNote: %v", err)
		}
	}

	got, _ := s.ReadNotes(ctx, "eng-2", "h1")
	var parsed struct {
		Notes []json.RawMessage `json:"notes"`
	}
	_ = json.Unmarshal(got, &parsed)
	if len(parsed.Notes) != 5 {
		t.Fatalf("无 Compactor 应 LTRIM 保留 5 条末尾，实际 %d: %s", len(parsed.Notes), got)
	}
	if string(parsed.Notes[0]) != `{"content":"n5","agent_run_id":"t"}` {
		t.Fatalf("应保留 n5..n9，首条实际 %s", parsed.Notes[0])
	}
}

// TestCompactorTriggered 注入 Compactor，超阈值后触发蒸馏：
//   - Compactor 被调用 1 次
//   - 前 batch 条被替换为 1 条 summary
//   - 列表 = [summary] + [剩余 entries]
func TestCompactorTriggered(t *testing.T) {
	fc := &fakeCompactor{
		out: []byte(`{"content":"[summary] merged","agent_run_id":"compactor"}`),
	}
	s, _ := newTestStore(t, Config{
		KeyPrefix:        "test:note:",
		MaxEntries:       5,
		CompactThreshold: 5,
		CompactBatchSize: 3,
		CompactTimeout:   5 * time.Second,
	})
	s.WithCompactor(fc)
	ctx := context.Background()

	for i := 0; i < 6; i++ {
		e := []byte(fmt.Sprintf(`{"content":"n%d","agent_run_id":"t"}`, i))
		if err := s.AppendNote(ctx, "eng-3", "h1", e); err != nil {
			t.Fatalf("AppendNote: %v", err)
		}
	}

	if got := atomic.LoadInt32(&fc.calls); got != 1 {
		t.Fatalf("Compactor 应被调 1 次，实际 %d", got)
	}

	got, _ := s.ReadNotes(ctx, "eng-3", "h1")
	var parsed struct {
		Notes []json.RawMessage `json:"notes"`
	}
	_ = json.Unmarshal(got, &parsed)

	// 6 条写入 - 3 条 batch 蒸馏 + 1 条 summary = 4 条
	if len(parsed.Notes) != 4 {
		t.Fatalf("蒸馏后应为 1 summary + 3 末尾，共 4 条，实际 %d: %s", len(parsed.Notes), got)
	}
	if string(parsed.Notes[0]) != `{"content":"[summary] merged","agent_run_id":"compactor"}` {
		t.Fatalf("首条应为 summary，实际 %s", parsed.Notes[0])
	}
	if string(parsed.Notes[1]) != `{"content":"n3","agent_run_id":"t"}` {
		t.Fatalf("第 2 条应为 n3（被蒸馏后剩 n3..n5），实际 %s", parsed.Notes[1])
	}
}

// TestCompactorFailureFallback Compactor 返 err 时退化为 LTRIM 末尾 MaxEntries。
func TestCompactorFailureFallback(t *testing.T) {
	fc := &fakeCompactor{err: fmt.Errorf("LLM 抖动")}
	s, _ := newTestStore(t, Config{
		KeyPrefix:        "test:note:",
		MaxEntries:       4,
		CompactThreshold: 5,
		CompactBatchSize: 3,
		CompactTimeout:   5 * time.Second,
	})
	s.WithCompactor(fc)
	ctx := context.Background()

	for i := 0; i < 6; i++ {
		e := []byte(fmt.Sprintf(`{"content":"n%d","agent_run_id":"t"}`, i))
		_ = s.AppendNote(ctx, "eng-4", "h1", e)
	}

	if atomic.LoadInt32(&fc.calls) == 0 {
		t.Fatal("Compactor 应被调（即便失败）")
	}

	got, _ := s.ReadNotes(ctx, "eng-4", "h1")
	var parsed struct {
		Notes []json.RawMessage `json:"notes"`
	}
	_ = json.Unmarshal(got, &parsed)
	if len(parsed.Notes) != 4 {
		t.Fatalf("Compactor 失败应 LTRIM 到 MaxEntries=4，实际 %d: %s", len(parsed.Notes), got)
	}
	if string(parsed.Notes[0]) != `{"content":"n2","agent_run_id":"t"}` {
		t.Fatalf("应保留末尾 n2..n5，首条实际 %s", parsed.Notes[0])
	}
}

// TestConcurrentCompactionLock 并发触发蒸馏时，SETNX 锁防止 Compactor 被重复调用。
//
// 验证：连续 6 次 AppendNote（前 5 条进入，第 6 条超阈值触发首次蒸馏），
// Compactor 总调用应 = 1。即便多次 AppendNote 都满足"LLEN > threshold"条件，
// 同一时间内只能有一个蒸馏在进行（锁保护 + 蒸馏后 list 已收缩，后续 AppendNote 不再超阈值）。
func TestConcurrentCompactionLock(t *testing.T) {
	fc := &fakeCompactor{
		delay: 100 * time.Millisecond,
		out:   []byte(`{"content":"[summary]","agent_run_id":"compactor"}`),
	}
	s, _ := newTestStore(t, Config{
		KeyPrefix:        "test:note:",
		MaxEntries:       4,
		CompactThreshold: 5,
		CompactBatchSize: 3,
		CompactTimeout:   5 * time.Second,
	})
	s.WithCompactor(fc)
	ctx := context.Background()

	for i := 0; i < 6; i++ {
		_ = s.AppendNote(ctx, "eng-5", "h1", []byte(fmt.Sprintf(`{"content":"x%d","agent_run_id":"t"}`, i)))
	}
	calls := atomic.LoadInt32(&fc.calls)
	if calls > 1 {
		t.Fatalf("并发蒸馏锁应阻止重复调用，实际调 %d 次", calls)
	}
}

// TestTTLSetOnceNotRefreshed 验证 ExpireNX 语义：TTL 仅首次设。
func TestTTLSetOnceNotRefreshed(t *testing.T) {
	s, mr := newTestStore(t, Config{KeyPrefix: "test:note:", TTL: 10 * time.Second})
	ctx := context.Background()

	const k = "test:note:eng-6:h1"
	if err := s.AppendNote(ctx, "eng-6", "h1", []byte(`{"content":"a"}`)); err != nil {
		t.Fatal(err)
	}
	ttl1 := mr.TTL(k)
	if ttl1 <= 0 || ttl1 > 10*time.Second {
		t.Fatalf("首次 TTL 期望 ≤10s，实际 %v", ttl1)
	}

	mr.FastForward(4 * time.Second)
	if err := s.AppendNote(ctx, "eng-6", "h1", []byte(`{"content":"b"}`)); err != nil {
		t.Fatal(err)
	}
	ttl2 := mr.TTL(k)
	if ttl2 > 6*time.Second {
		t.Fatalf("ExpireNX 应不刷新 TTL，期望 ≤6s，实际 %v", ttl2)
	}
	if ttl2 <= 0 {
		t.Fatalf("第二次写入后 TTL 不应消失，实际 %v", ttl2)
	}

	mr.FastForward(7 * time.Second)
	if mr.Exists(k) {
		t.Fatalf("过 TTL 后 key 应消失")
	}
}

// TestReadEmptyKey key 不存在时 ReadNotes 返回 {"notes":[]} 不报错。
func TestReadEmptyKey(t *testing.T) {
	s, _ := newTestStore(t, Config{KeyPrefix: "test:note:"})
	ctx := context.Background()

	got, err := s.ReadNotes(ctx, "eng-empty", "h1")
	if err != nil {
		t.Fatalf("空 key 不应报错: %v", err)
	}
	want := `{"notes":[]}`
	if string(got) != want {
		t.Fatalf("空 key 期望 %s，实际 %s", want, got)
	}
}

// TestEmptyArgs engagementID / host / entry 任一为空时返回错误。
func TestEmptyArgs(t *testing.T) {
	s, _ := newTestStore(t, Config{KeyPrefix: "test:note:"})
	ctx := context.Background()

	if err := s.AppendNote(ctx, "", "h1", []byte("x")); err == nil {
		t.Fatal("空 engagementID 应报错")
	}
	if err := s.AppendNote(ctx, "eng", "", []byte("x")); err == nil {
		t.Fatal("空 host 应报错")
	}
	if err := s.AppendNote(ctx, "eng", "h1", nil); err == nil {
		t.Fatal("空 entry 应报错")
	}
	if _, err := s.ReadNotes(ctx, "", "h1"); err == nil {
		t.Fatal("空 engagementID 读取应报错")
	}
	if _, err := s.ReadNotes(ctx, "eng", ""); err == nil {
		t.Fatal("空 host 读取应报错")
	}
}

// TestFallbackDefaults Config 全零时走 fallback 常量（key 前缀、TTL）。
func TestFallbackDefaults(t *testing.T) {
	s, mr := newTestStore(t, Config{})
	ctx := context.Background()

	_ = s.AppendNote(ctx, "eng-7", "h1", []byte(`{"content":"x"}`))
	if !mr.Exists("liusha:note:eng-7:h1") {
		t.Fatal("空 Config 应使用 fallbackKeyPrefix=liusha:note:")
	}
	ttl := mr.TTL("liusha:note:eng-7:h1")
	if ttl <= 23*time.Hour || ttl > 24*time.Hour {
		t.Fatalf("空 Config TTL 应≈24h，实际 %v", ttl)
	}
}
