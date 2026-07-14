//go:build integration

package corpus

import (
	"context"
	"testing"

	"github.com/V3teran/liusha/internal/dbtest"
)

// TestStore_AddThenSearch 验证：写入后能被 sparse（pg_trgm）检索到（queryVec 空 → 纯 sparse 降级路径）。
func TestStore_AddThenSearch(t *testing.T) {
	pool := dbtest.NewPgPool(t)
	s := NewStore(pool)
	ctx := context.Background()

	if _, err := s.Add(ctx, Entry{
		Title:   "CAS SSO 登录逆向",
		Content: "逆向前端登录 js 拿加密逻辑，凡用此 CAS 的系统皆可复用",
		Tags:    []string{"sso:cas"},
		Source:  SourceExpert,
	}); err != nil {
		t.Fatalf("add: %v", err)
	}

	// queryVec=nil、rr=nil → SearchHybrid 退纯 sparse + 合并序兜底。
	got, err := s.SearchHybrid(ctx, "CAS SSO 登录", nil, nil, 20, 5, nil)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("sparse 应命中含『CAS SSO 登录』的条目")
	}
	if got[0].Source != SourceExpert {
		t.Fatalf("字段不匹配: %+v", got[0])
	}
}

// TestStore_Add_DedupsByContentHash 验证：同 content 重复写去重（hit_count++，不新增行）。
func TestStore_Add_DedupsByContentHash(t *testing.T) {
	pool := dbtest.NewPgPool(t)
	s := NewStore(pool)
	ctx := context.Background()

	first, err := s.Add(ctx, Entry{Content: "同一条知识", Source: SourceAgent})
	if err != nil {
		t.Fatalf("add 1: %v", err)
	}
	second, err := s.Add(ctx, Entry{Content: "同一条知识", Source: SourceAgent})
	if err != nil {
		t.Fatalf("add 2: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("同 content 应命中同一行去重: %s != %s", first.ID, second.ID)
	}
	if second.HitCount != first.HitCount+1 {
		t.Fatalf("重复写应 hit_count++: %d → %d", first.HitCount, second.HitCount)
	}
}

// TestStore_Search_TagsFilter 验证：给了 tags 时按标签硬过滤，不匹配的不返回。
func TestStore_Search_TagsFilter(t *testing.T) {
	pool := dbtest.NewPgPool(t)
	s := NewStore(pool)
	ctx := context.Background()

	if _, err := s.Add(ctx, Entry{Content: "shiro 反序列化利用链", Tags: []string{"shiro"}, Source: SourceExpert}); err != nil {
		t.Fatalf("add shiro: %v", err)
	}
	if _, err := s.Add(ctx, Entry{Content: "fastjson 反序列化利用链", Tags: []string{"fastjson"}, Source: SourceExpert}); err != nil {
		t.Fatalf("add fastjson: %v", err)
	}

	// 查"反序列化"但限定 tag=shiro → 只应回 shiro 那条。
	got, err := s.SearchHybrid(ctx, "反序列化利用链", nil, []string{"shiro"}, 20, 5, nil)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	for _, e := range got {
		if !contains(e.Tags, "shiro") {
			t.Fatalf("tags 过滤失效，返回了非 shiro 条目: %+v", e)
		}
	}
	if len(got) == 0 {
		t.Fatal("应命中 shiro 那条")
	}
}

func contains(ss []string, target string) bool {
	for _, s := range ss {
		if s == target {
			return true
		}
	}
	return false
}

// TestStore_Add_RejectsBadSource 验证：非法 source 建不出。
func TestStore_Add_RejectsBadSource(t *testing.T) {
	pool := dbtest.NewPgPool(t)
	s := NewStore(pool)
	if _, err := s.Add(context.Background(), Entry{Content: "x", Source: "bogus"}); err == nil {
		t.Fatal("非法 source 应报错")
	}
}
