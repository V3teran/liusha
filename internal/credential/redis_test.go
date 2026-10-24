//go:build integration

package credential

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"

	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/db"
)

// newProvider 启动一次性 Redis 容器并返回 RedisProvider，t.Cleanup 自动回收。
func newProvider(t *testing.T) *RedisProvider {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)

	c, err := tcredis.Run(ctx, "redis:8")
	if err != nil {
		t.Fatalf("启动 redis 容器失败: %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(ctx) })

	connStr, err := c.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("获取 redis 连接串失败: %v", err)
	}
	client, err := db.NewRedis(ctx, strings.TrimPrefix(connStr, "redis://"), config.RedisConfig{})
	if err != nil {
		t.Fatalf("连接 redis 失败: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	return NewRedis(client, "")
}

// names 提取 identity 列表的 Name 并排序，便于断言。
func names(ids []Identity) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, id.Username)
	}
	sort.Strings(out)
	return out
}

// equalSlices 对比两个字符串切片（已排序）。
func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestRedis_BatchSaveAndGet 验证：存 2 个 host 各 2 个 identity，按 host 取回；
// 每个 host 自动注入 anonymous（共 3 个 identity）。
func TestRedis_BatchSaveAndGet(t *testing.T) {
	ctx := context.Background()
	p := newProvider(t)

	in := map[string][]Identity{
		"vulnapp": {
			{Username: "admin", Role: "admin", Credentials: []Credential{{Type: TypeHeaders, Key: "Cookie", Value: "session=admin_sess"}}},
			{Username: "user", Role: "user", Credentials: []Credential{{Type: TypeHeaders, Key: "Cookie", Value: "session=user_sess"}}},
		},
		"another": {
			{Username: "alice", Role: "admin", Credentials: []Credential{{Type: TypeQuery, Key: "token", Value: "alice_tk"}}},
			{Username: "bob", Role: "user", Credentials: []Credential{{Type: TypeBody, Key: "auth", Value: "bob_tk"}}},
		},
	}
	for host, ids := range in {
		if err := p.SaveIdentities(ctx, host, ids); err != nil {
			t.Fatalf("save %s: %v", host, err)
		}
	}

	got1, err := p.GetIdentitiesByHost(ctx, "vulnapp")
	if err != nil {
		t.Fatalf("get vulnapp: %v", err)
	}
	if !equalSlices(names(got1), []string{"admin", "user"}) {
		t.Fatalf("vulnapp 身份应为 [admin, user], got=%v", names(got1))
	}

	got2, err := p.GetIdentitiesByHost(ctx, "another")
	if err != nil {
		t.Fatalf("get another: %v", err)
	}
	if !equalSlices(names(got2), []string{"alice", "bob"}) {
		t.Fatalf("another 身份应为 [alice, bob], got=%v", names(got2))
	}

	// 校验 credential 字段被正确反序列化。
	for _, id := range got1 {
		if id.Username == "admin" {
			if len(id.Credentials) != 1 || id.Credentials[0].Value != "session=admin_sess" {
				t.Fatalf("admin credentials 反序列化错误: %+v", id.Credentials)
			}
		}
	}
}

// TestRedis_PermanentStorage 验证永久存储约定：任何时长后凭证仍在——
// 本库零过期机制，生命周期由任务终态清理/清扫器/人工删除驱动。
func TestRedis_PermanentStorage(t *testing.T) {
	ctx := context.Background()
	p := newProvider(t)

	if err := p.SaveIdentities(ctx, "h", []Identity{{Username: "u", Role: "user"}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	time.Sleep(1100 * time.Millisecond)

	got, err := p.GetIdentitiesByHost(ctx, "h")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !equalSlices(names(got), []string{"u"}) {
		t.Fatalf("无过期机制——任何时长后凭证应仍在, got=%v", names(got))
	}
}

// TestRedis_NoExpiryKey 验证键上无任何 TTL（PTTL=-1）——零过期是本库的
// 硬约定，存储层不得偷偷挂 EXPIRE。
func TestRedis_NoExpiryKey(t *testing.T) {
	ctx := context.Background()
	p := newProvider(t)

	if err := p.SaveIdentities(ctx, "perm", []Identity{{Username: "alice", Role: "admin"}}); err != nil {
		t.Fatalf("save: %v", err)
	}

	d, err := p.client.TTL(ctx, p.hashKey("perm")).Result()
	if err != nil {
		t.Fatalf("ttl: %v", err)
	}
	// go-redis 在 key 永久不过期时返回 -1ns。
	if d != -1*time.Nanosecond {
		t.Fatalf("ttl 应为 -1（永久），got=%v", d)
	}
}

// TestRedis_Delete 验证：删 host 后取空。
func TestRedis_Delete(t *testing.T) {
	ctx := context.Background()
	p := newProvider(t)

	if err := p.SaveIdentities(ctx, "target", []Identity{
		{Username: "a", Role: "admin"},
		{Username: "b", Role: "user"},
	}); err != nil {
		t.Fatalf("batch save: %v", err)
	}

	if err := p.DeleteHost(ctx, "target"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	got, err := p.GetIdentitiesByHost(ctx, "target")
	if err != nil {
		t.Fatalf("get after delete: %v", err)
	}
	if !equalSlices(names(got), []string{}) {
		t.Fatalf("删除后应为空, got=%v", names(got))
	}
}

// TestRedis_SaveIdentities_OverwriteExisting 验证：重复保存同 host 同 name 覆盖旧值。
func TestRedis_SaveIdentities_OverwriteExisting(t *testing.T) {
	ctx := context.Background()
	p := newProvider(t)

	if err := p.SaveIdentities(ctx, "h", []Identity{{Username: "u", Role: "user", Credentials: []Credential{{Type: TypeHeaders, Key: "X-Token", Value: "v1"}}}}); err != nil {
		t.Fatalf("first save: %v", err)
	}

	if err := p.SaveIdentities(ctx, "h", []Identity{{Username: "u", Role: "admin", Credentials: []Credential{{Type: TypeHeaders, Key: "X-Token", Value: "v2"}}}}); err != nil {
		t.Fatalf("second save: %v", err)
	}

	got, err := p.GetIdentitiesByHost(ctx, "h")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	var found bool
	for _, id := range got {
		if id.Username == "u" {
			found = true
			if id.Role != "admin" {
				t.Fatalf("覆盖后 role 应为 admin, got=%s", id.Role)
			}
			if len(id.Credentials) != 1 || id.Credentials[0].Value != "v2" {
				t.Fatalf("覆盖后 credential 应为 v2, got=%+v", id.Credentials)
			}
		}
	}
	if !found {
		t.Fatalf("未找到覆盖后的 u, got=%v", names(got))
	}
}

// TestRedis_SaveIdentities_SkipsAnonymous 验证：调用方传入 anonymous 时不持久化（避免污染存储）。
func TestRedis_SaveIdentities_SkipsAnonymous(t *testing.T) {
	ctx := context.Background()
	p := newProvider(t)

	if err := p.SaveIdentities(ctx, "h", []Identity{
		{Username: AnonymousName, Role: "anonymous"},
		{Username: "real", Role: "user"},
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	// 直接探测 hash field：anonymous field 不应被写入（hash 本身因为 real 存在）。
	exists, err := p.client.HExists(ctx, p.hashKey("h"), AnonymousName).Result()
	if err != nil {
		t.Fatalf("hexists: %v", err)
	}
	if exists {
		t.Fatalf("anonymous field 不应被持久化")
	}

	got, err := p.GetIdentitiesByHost(ctx, "h")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !equalSlices(names(got), []string{"real"}) {
		t.Fatalf("应为 [real], got=%v", names(got))
	}
}
