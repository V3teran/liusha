package lead

import (
	"context"
	"fmt"
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
	return NewStore(rdb, "test", time.Hour), mr
}

// Append 一条 clue 后 ReadRecent 应能读回，字段完整。
func TestStore_AppendThenReadRecent(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()

	if err := s.Append(ctx, "target.com", Entry{
		Kind: KindClue, Detail: "登录页有隐藏调试参数 debug=1", HunterID: "h1", SourceTaskID: "t1",
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
	if clues[0].Detail != "登录页有隐藏调试参数 debug=1" || clues[0].HunterID != "h1" || clues[0].SourceTaskID != "t1" {
		t.Fatalf("字段不匹配: %+v", clues[0])
	}
}

// Append 拒绝非法 kind 与空 note。
func TestStore_Append_Validates(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()

	if err := s.Append(ctx, "target.com", Entry{Kind: "bogus", Detail: "x"}); err == nil {
		t.Fatal("非法 kind 应报错")
	}
	if err := s.Append(ctx, "target.com", Entry{Kind: KindFact, Detail: ""}); err == nil {
		t.Fatal("空 note 应报错")
	}
}

// 读时按 (host, kind) 分组，每 kind 最多 perKindLimit 条，且是最近写入的那些（新压旧）。
// 读时按 kind 分组，全量返回（不再截断），且 detail 完全相同的条目精确去重（只留一条）。
func TestStore_ReadRecent_GroupsAllAndDedups(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()

	// 同一句 clue 复写 8 遍 → 去重后只剩 1 条。
	for i := 0; i < 8; i++ {
		if err := s.Append(ctx, "target.com", Entry{
			Kind: KindClue, Detail: "重复线索", SourceTaskID: "t1",
		}); err != nil {
			t.Fatalf("append dup clue #%d: %v", i, err)
		}
	}
	// 3 条各不相同的 clue → 全量保留（验证不再截断到 5 之类）。
	for i := 0; i < 3; i++ {
		if err := s.Append(ctx, "target.com", Entry{
			Kind: KindClue, Detail: fmt.Sprintf("独立线索-%d", i), SourceTaskID: "t1",
		}); err != nil {
			t.Fatalf("append uniq clue #%d: %v", i, err)
		}
	}
	if err := s.Append(ctx, "target.com", Entry{Kind: KindFact, Detail: "fact-1", SourceTaskID: "t1"}); err != nil {
		t.Fatalf("append fact: %v", err)
	}

	got, err := s.ReadRecent(ctx, "target.com")
	if err != nil {
		t.Fatalf("read recent: %v", err)
	}
	// clue：1 条去重后的"重复线索" + 3 条独立线索 = 4 条（不是 8+3=11，也不是被截断到 5）。
	if len(got[KindClue]) != 4 {
		t.Fatalf("clue 数量=%d，期望去重后 4 条（1 重复 + 3 独立）", len(got[KindClue]))
	}
	if len(got[KindFact]) != 1 || got[KindFact][0].Detail != "fact-1" {
		t.Fatalf("fact 不匹配: %+v", got[KindFact])
	}
}

// 不同 host 的黑板互相独立。
func TestStore_ReadRecent_IndependentPerHost(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()

	if err := s.Append(ctx, "a.com", Entry{Kind: KindClue, Detail: "a-clue"}); err != nil {
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
		if err := s.Append(ctx, "target.com", Entry{Kind: KindClue, Detail: "clue"}); err != nil {
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

// Append 每次写都滚动刷新该 host 的 key TTL（ttl>0 时）。
func TestStore_Append_RefreshesTTL(t *testing.T) {
	s, mr := newTestStore(t) // newTestStore 用 ttl=time.Hour
	ctx := context.Background()

	if err := s.Append(ctx, "target.com", Entry{Kind: KindFact, Detail: "x"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if ttl := mr.TTL(s.key("target.com")); ttl <= 0 {
		t.Fatalf("首次写后 TTL 应 > 0，got %v", ttl)
	}

	// 手动把 TTL 缩短，再写一条 → 应被刷回接近 ttl（证明滚动续命）。
	mr.SetTTL(s.key("target.com"), time.Minute)
	if err := s.Append(ctx, "target.com", Entry{Kind: KindClue, Detail: "y"}); err != nil {
		t.Fatalf("append 2: %v", err)
	}
	if ttl := mr.TTL(s.key("target.com")); ttl <= time.Minute {
		t.Fatalf("再次写后 TTL 应被刷新回接近 1h（> 1min），got %v", ttl)
	}
}

// ttl≤0 时 Append 不设过期（永不过期，仅靠 LTRIM 兜底）。
func TestStore_Append_NoTTLWhenDisabled(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	s := NewStore(rdb, "test", 0) // ttl=0 关过期

	ctx := context.Background()
	if err := s.Append(ctx, "target.com", Entry{Kind: KindFact, Detail: "x"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if ttl := mr.TTL(s.key("target.com")); ttl != 0 {
		t.Fatalf("ttl=0 时不应设过期，got %v", ttl)
	}
}

// 反序列化失败的脏条目容错跳过，不阻塞其余条目读取。
func TestStore_ReadRecent_SkipsCorruptEntries(t *testing.T) {
	s, mr := newTestStore(t)
	ctx := context.Background()

	if err := s.Append(ctx, "target.com", Entry{Kind: KindClue, Detail: "good"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if _, err := mr.Lpush(s.key("target.com"), "not-json"); err != nil {
		t.Fatalf("lpush corrupt: %v", err)
	}

	got, err := s.ReadRecent(ctx, "target.com")
	if err != nil {
		t.Fatalf("read recent: %v", err)
	}
	if len(got[KindClue]) != 1 || got[KindClue][0].Detail != "good" {
		t.Fatalf("应容错跳过脏条目只留 good: %+v", got)
	}
}
