package notes

import (
	"context"
	"encoding/json"
	"fmt"
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
		if err := s.AppendNote(ctx, "eng-1", e); err != nil {
			t.Fatalf("AppendNote: %v", err)
		}
	}

	got, err := s.ReadNotes(ctx, "eng-1")
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

// TestLTrimEnforcesMaxEntries 连续写入超过 MaxEntries 时，只保留末尾 N 条；
// ReadNotes 取全部即得 N 条（无二层截断）。
func TestLTrimEnforcesMaxEntries(t *testing.T) {
	s, _ := newTestStore(t, Config{KeyPrefix: "test:note:", MaxEntries: 5})
	ctx := context.Background()

	for i := 0; i < 10; i++ {
		e := []byte(fmt.Sprintf(`{"content":"n%d","agent_run_id":"t"}`, i))
		if err := s.AppendNote(ctx, "eng-2", e); err != nil {
			t.Fatalf("AppendNote: %v", err)
		}
	}

	got, _ := s.ReadNotes(ctx, "eng-2")
	var parsed struct {
		Notes []json.RawMessage `json:"notes"`
	}
	_ = json.Unmarshal(got, &parsed)
	if len(parsed.Notes) != 5 {
		t.Fatalf("LTRIM 应保留 5 条末尾，实际 %d: %s", len(parsed.Notes), got)
	}
	first := string(parsed.Notes[0])
	if first != `{"content":"n5","agent_run_id":"t"}` {
		t.Fatalf("LTRIM 应保留末尾 n5..n9，首条实际 %s", first)
	}
}

// TestTTLSetOnceNotRefreshed 验证 ExpireNX 语义：
//   - 首次 AppendNote 后 key TTL ≈ 配置值
//   - 推进 4s 后再次 AppendNote，TTL 应只剩 ≤6s（不被刷新到 10s）
//   - 推进总过 TTL 后 key 消失
//
// 这保证 notes 寿命从 key 创建时刻起算固定窗口，与 engagement.MaxAge 严格对齐。
func TestTTLSetOnceNotRefreshed(t *testing.T) {
	s, mr := newTestStore(t, Config{KeyPrefix: "test:note:", TTL: 10 * time.Second})
	ctx := context.Background()

	if err := s.AppendNote(ctx, "eng-4", []byte(`{"content":"a"}`)); err != nil {
		t.Fatal(err)
	}
	ttl1 := mr.TTL("test:note:eng-4")
	if ttl1 <= 0 || ttl1 > 10*time.Second {
		t.Fatalf("首次 TTL 期望 ≤10s，实际 %v", ttl1)
	}

	mr.FastForward(4 * time.Second)
	if err := s.AppendNote(ctx, "eng-4", []byte(`{"content":"b"}`)); err != nil {
		t.Fatal(err)
	}
	ttl2 := mr.TTL("test:note:eng-4")
	if ttl2 > 6*time.Second {
		t.Fatalf("ExpireNX 应不刷新 TTL，期望 ≤6s，实际 %v（被刷新了）", ttl2)
	}
	if ttl2 <= 0 {
		t.Fatalf("第二次写入后 TTL 不应消失，实际 %v", ttl2)
	}

	mr.FastForward(7 * time.Second)
	if mr.Exists("test:note:eng-4") {
		t.Fatalf("过 TTL 后 key 应消失")
	}
}

// TestReadEmptyKey key 不存在时 ReadNotes 返回 {"notes":[]} 不报错。
func TestReadEmptyKey(t *testing.T) {
	s, _ := newTestStore(t, Config{KeyPrefix: "test:note:"})
	ctx := context.Background()

	got, err := s.ReadNotes(ctx, "eng-empty")
	if err != nil {
		t.Fatalf("空 key 不应报错: %v", err)
	}
	want := `{"notes":[]}`
	if string(got) != want {
		t.Fatalf("空 key 期望 %s，实际 %s", want, got)
	}
}

// TestEmptyArgs engagementID 或 entry 为空时返回错误。
func TestEmptyArgs(t *testing.T) {
	s, _ := newTestStore(t, Config{KeyPrefix: "test:note:"})
	ctx := context.Background()

	if err := s.AppendNote(ctx, "", []byte("x")); err == nil {
		t.Fatal("空 engagementID 应报错")
	}
	if err := s.AppendNote(ctx, "eng", nil); err == nil {
		t.Fatal("空 entry 应报错")
	}
	if _, err := s.ReadNotes(ctx, ""); err == nil {
		t.Fatal("空 engagementID 读取应报错")
	}
}

// TestFallbackDefaults Config 全零时走 fallback 常量（key 前缀、TTL）。
func TestFallbackDefaults(t *testing.T) {
	s, mr := newTestStore(t, Config{})
	ctx := context.Background()

	_ = s.AppendNote(ctx, "eng-6", []byte(`{"content":"x"}`))
	if !mr.Exists("liusha:note:eng-6") {
		t.Fatal("空 Config 应使用 fallbackKeyPrefix=liusha:note:")
	}
	ttl := mr.TTL("liusha:note:eng-6")
	if ttl <= 23*time.Hour || ttl > 24*time.Hour {
		t.Fatalf("空 Config TTL 应≈24h，实际 %v", ttl)
	}
}
