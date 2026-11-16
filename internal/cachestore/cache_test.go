package cachestore

import (
	"context"
	"errors"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// 迁移会改写数据库里被缓存的数据：升级后（数据库结构版本前进）的进程不能读到迁移前写进 L2 的
// 旧副本；同一版本的进程之间照常共享 L2。
func TestReadThrough_L2IsScopedBySchemaVersion(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	ctx := context.Background()
	read := func(c *Cache, src string) string {
		t.Helper()
		v, err := ReadThrough(ctx, c, "settingstore:override:g",
			func(string) []string { return []string{"settingstore:override:g"} },
			func(context.Context) (string, error) {
				if src == "" {
					return "", errors.New("不应回源")
				}
				return src, nil
			})
		if err != nil {
			t.Fatal(err)
		}
		return v
	}

	if got := read(New(rdb, 231), "迁移前"); got != "迁移前" {
		t.Fatalf("got %q", got)
	}
	if got := read(New(rdb, 232), "迁移后"); got != "迁移后" {
		t.Fatalf("新版本的进程读到了迁移前的 L2 副本: %q", got)
	}
	// 同版本的另一个进程（L1 为空）命中 L2，不回源。
	if got := read(New(rdb, 232), ""); got != "迁移后" {
		t.Fatalf("同版本进程应共享 L2: %q", got)
	}
}
