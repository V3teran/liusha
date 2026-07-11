package lead

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newTestStore(t *testing.T) (*Store, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return NewStore(rdb, "test"), mr
}

// Append 一条 clue 后 ReadRecent 应能读回，字段完整。
func TestStore_AppendThenReadRecent(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()

	if err := s.Append(ctx, "target.com", Entry{
		Kind: KindClue, Note: "登录页有隐藏调试参数 debug=1", HunterID: "h1", SourceTaskID: "t1",
	}); err != nil {
		t.Fatalf("append: %v", err)
	}

	got, err := s.ReadRecent(ctx, "target.com")
	if err != nil {
		t.Fatalf("read recent: %v", err)
	}
	clues := got[KindClue]
	if len(clues) != 1 {
		t.Fatalf("clues 数量=%d，期望 1", len(clues))
	}
	if clues[0].Note != "登录页有隐藏调试参数 debug=1" || clues[0].HunterID != "h1" || clues[0].SourceTaskID != "t1" {
		t.Fatalf("字段不匹配: %+v", clues[0])
	}
}

// Append 拒绝非法 kind 与空 note。
func TestStore_Append_Validates(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()

	if err := s.Append(ctx, "target.com", Entry{Kind: "bogus", Note: "x"}); err == nil {
		t.Fatal("非法 kind 应报错")
	}
	if err := s.Append(ctx, "target.com", Entry{Kind: KindFact, Note: ""}); err == nil {
		t.Fatal("空 note 应报错")
	}
}

// 读时按 (host, kind) 分组，每 kind 最多 perKindLimit 条，且是最近写入的那些（新压旧）。
func TestStore_ReadRecent_GroupsByKindAndCaps(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()

	for i := 0; i < perKindLimit+3; i++ {
		if err := s.Append(ctx, "target.com", Entry{
			Kind: KindClue, Note: "clue", SourceTaskID: "t1",
		}); err != nil {
			t.Fatalf("append clue #%d: %v", i, err)
		}
	}
	if err := s.Append(ctx, "target.com", Entry{Kind: KindFact, Note: "fact-1", SourceTaskID: "t1"}); err != nil {
		t.Fatalf("append fact: %v", err)
	}
	if err := s.Append(ctx, "target.com", Entry{Kind: KindDeadend, Note: "deadend-1", SourceTaskID: "t1"}); err != nil {
		t.Fatalf("append deadend: %v", err)
	}

	got, err := s.ReadRecent(ctx, "target.com")
	if err != nil {
		t.Fatalf("read recent: %v", err)
	}
	if len(got[KindClue]) != perKindLimit {
		t.Fatalf("clue 数量=%d，期望截断到 %d", len(got[KindClue]), perKindLimit)
	}
	if len(got[KindFact]) != 1 || got[KindFact][0].Note != "fact-1" {
		t.Fatalf("fact 不匹配: %+v", got[KindFact])
	}
	if len(got[KindDeadend]) != 1 || got[KindDeadend][0].Note != "deadend-1" {
		t.Fatalf("deadend 不匹配: %+v", got[KindDeadend])
	}
}

// 不同 host 的黑板互相独立。
func TestStore_ReadRecent_IndependentPerHost(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()

	if err := s.Append(ctx, "a.com", Entry{Kind: KindClue, Note: "a-clue"}); err != nil {
		t.Fatalf("append a: %v", err)
	}
	got, err := s.ReadRecent(ctx, "b.com")
	if err != nil {
		t.Fatalf("read b: %v", err)
	}
	if len(got[KindClue]) != 0 {
		t.Fatalf("b.com 不应看到 a.com 的情报: %+v", got)
	}
}

// Append 超过 maxEntries 后，LIST 应被 LTRIM 到 maxEntries（防单 host 无界增长）。
func TestStore_Append_TrimsToMaxEntries(t *testing.T) {
	s, mr := newTestStore(t)
	ctx := context.Background()

	for i := 0; i < maxEntries+10; i++ {
		if err := s.Append(ctx, "target.com", Entry{Kind: KindClue, Note: "clue"}); err != nil {
			t.Fatalf("append #%d: %v", i, err)
		}
	}
	n, err := mr.List(s.key("target.com"))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(n) != maxEntries {
		t.Fatalf("list 长度=%d，期望裁剪到 %d", len(n), maxEntries)
	}
}

// ExpireHost 对该 host 设 TTL 后，key 应带过期时间。
func TestStore_ExpireHost(t *testing.T) {
	s, mr := newTestStore(t)
	ctx := context.Background()

	if err := s.Append(ctx, "target.com", Entry{Kind: KindFact, Note: "x"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := s.ExpireHost(ctx, "target.com", time.Hour); err != nil {
		t.Fatalf("expire: %v", err)
	}
	ttl := mr.TTL(s.key("target.com"))
	if ttl <= 0 {
		t.Fatalf("expire 后 TTL 应 > 0，got %v", ttl)
	}
}

// 反序列化失败的脏条目容错跳过，不阻塞其余条目读取。
func TestStore_ReadRecent_SkipsCorruptEntries(t *testing.T) {
	s, mr := newTestStore(t)
	ctx := context.Background()

	if err := s.Append(ctx, "target.com", Entry{Kind: KindClue, Note: "good"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if _, err := mr.Lpush(s.key("target.com"), "not-json"); err != nil {
		t.Fatalf("lpush corrupt: %v", err)
	}

	got, err := s.ReadRecent(ctx, "target.com")
	if err != nil {
		t.Fatalf("read recent: %v", err)
	}
	if len(got[KindClue]) != 1 || got[KindClue][0].Note != "good" {
		t.Fatalf("应容错跳过脏条目只留 good: %+v", got)
	}
}
