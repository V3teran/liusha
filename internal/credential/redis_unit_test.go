package credential

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newTestProvider(t *testing.T) (*RedisProvider, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return NewRedis(client, ""), mr
}

// TestSessionIdentityNaming 锁定会话身份命名权威：名字是身份类别的唯一
// 事实源（存储层不存类别标记），判别/溯源必须与命名规则一致。
func TestSessionIdentityNaming(t *testing.T) {
	if got := SessionIdentityName("t-123"); got != "task:t-123:session" {
		t.Fatalf("任务会话名: %q", got)
	}
	if got := SessionIdentityName(""); got != "session" {
		t.Fatalf("全局会话名: %q", got)
	}
	if !IsSessionIdentity("task:t-123:session") || !IsSessionIdentity("session") {
		t.Fatal("会话身份应判别为会话类")
	}
	if IsSessionIdentity("admin-low") || IsSessionIdentity("task:") || IsSessionIdentity("") {
		t.Fatal("持久/畸形名不得判为会话类")
	}
	if id, ok := SessionTaskID("task:t-123:session"); !ok || id != "t-123" {
		t.Fatalf("会话归属任务: id=%q ok=%v", id, ok)
	}
	if _, ok := SessionTaskID("admin-low"); ok {
		t.Fatal("持久身份无归属任务")
	}
	if _, ok := SessionTaskID("session"); ok {
		t.Fatal("全局会话无归属任务（ok 应为 false，交人工管理）")
	}
}

// TestSaveIdentitiesUpsertAndStamping 锁定 upsert 与录入时间语义：
// 首次写入打点、更新保留原录入时间、anonymous 防御性跳过。
func TestSaveIdentitiesUpsertAndStamping(t *testing.T) {
	p, _ := newTestProvider(t)
	ctx := context.Background()

	first := Identity{Username: "admin", Credentials: []Credential{{Type: TypeHeaders, Key: "Cookie", Value: "abc"}}}
	if err := p.SaveIdentities(ctx, "h.com:8080", []Identity{first}); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := p.GetIdentitiesByHost(ctx, "h.com:8080") // 端口保留：带端口读取命中同键
	if err != nil || len(got) != 1 {
		t.Fatalf("read: n=%d err=%v", len(got), err)
	}
	// 端口参与隔离：无端口的 h.com 是另一个应用（另一个键），互不可见
	if other, _ := p.GetIdentitiesByHost(ctx, "h.com"); len(other) != 0 {
		t.Fatalf("端口不同的键不得共享凭证: %+v", other)
	}
	if got[0].CreatedAt == nil || got[0].UpdatedAt == nil {
		t.Fatal("首次写入应打点 CreatedAt/UpdatedAt")
	}
	firstAt := *got[0].CreatedAt

	time.Sleep(time.Millisecond) // 保证时间可分辨
	first.Role = "admin-v2"
	if err := p.SaveIdentities(ctx, "h.com:8080", []Identity{first, {Username: AnonymousName}}); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ = p.GetIdentitiesByHost(ctx, "h.com:8080")
	if len(got) != 1 {
		t.Fatalf("anonymous 必须被跳过（n=%d）", len(got))
	}
	if got[0].Role != "admin-v2" {
		t.Fatal("upsert 未生效")
	}
	if !got[0].CreatedAt.Equal(firstAt) {
		t.Fatal("更新不得刷新录入时间")
	}
	if got[0].UpdatedAt == nil || !got[0].UpdatedAt.After(firstAt) {
		t.Fatalf("更新应刷新修改时间: created=%v updated=%v", firstAt, got[0].UpdatedAt)
	}
}

// TestSaveIdentities_NameNormalization 锁定身份名单点归一：大小写/空白
// 变体落到同一 field（唯一性不变式），不拆成两个身份。
func TestSaveIdentities_NameNormalization(t *testing.T) {
	p, _ := newTestProvider(t)
	ctx := context.Background()

	if err := p.SaveIdentities(ctx, "n.com", []Identity{{Username: "  Admin  "}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := p.SaveIdentities(ctx, "n.com", []Identity{{Username: "ADMIN", Role: "admin"}}); err != nil {
		t.Fatalf("save variant: %v", err)
	}
	ids, _ := p.GetIdentitiesByHost(ctx, "n.com")
	if len(ids) != 1 || ids[0].Username != "admin" || ids[0].Role != "admin" {
		t.Fatalf("变体应 upsert 同一身份: %+v", ids)
	}
}

// TestDeletePaths 锁定三条删除通道的边界：单删幂等、会话清理只动会话、
// 整 host 清空全部。
func TestDeletePaths(t *testing.T) {
	p, _ := newTestProvider(t)
	ctx := context.Background()
	if err := p.SaveIdentities(ctx, "t.com", []Identity{
		{Username: "admin"}, {Username: "user"},
		{Username: SessionIdentityName("t1")}, {Username: SessionIdentityName("t2")},
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	if err := p.DeleteIdentity(ctx, "t.com", "user"); err != nil {
		t.Fatalf("delete identity: %v", err)
	}
	if err := p.DeleteIdentity(ctx, "t.com", "user"); err != nil {
		t.Fatalf("删除不存在身份应幂等: %v", err)
	}

	n, err := p.DeleteSessions(ctx, "t.com")
	if err != nil || n != 2 {
		t.Fatalf("清理会话: n=%d err=%v", n, err)
	}
	ids, _ := p.GetIdentitiesByHost(ctx, "t.com")
	if len(ids) != 1 || ids[0].Username != "admin" {
		t.Fatalf("会话清理不得动持久身份: %+v", ids)
	}

	if err := p.DeleteHost(ctx, "t.com"); err != nil {
		t.Fatalf("delete host: %v", err)
	}
	if ids, _ := p.GetIdentitiesByHost(ctx, "t.com"); len(ids) != 0 {
		t.Fatalf("整 host 清空后应为空: %+v", ids)
	}
}

// TestListHostsClassification 锁定枚举口径：只认本前缀下的 hash 键、
// 按身份名分类计数、非 hash 键（历史借名残留）与空 hash 不上枚举。
func TestListHostsClassification(t *testing.T) {
	p, mr := newTestProvider(t)
	ctx := context.Background()
	if err := p.SaveIdentities(ctx, "a.com", []Identity{
		{Username: "admin"}, {Username: SessionIdentityName("t1")},
	}); err != nil {
		t.Fatalf("save a: %v", err)
	}
	if err := p.SaveIdentities(ctx, "b.com", []Identity{{Username: "guest"}}); err != nil {
		t.Fatalf("save b: %v", err)
	}
	// 借名键：同前缀但类型非 hash（历史信号量/聚合残留形状）
	if err := p.client.Set(ctx, "credentials:hostsem:a.com", "1", 0).Err(); err != nil {
		t.Fatalf("plant foreign key: %v", err)
	}
	// 空 hash：存在但无 field
	if err := p.client.HSet(ctx, p.hashKey("empty.com"), map[string]any{}).Err(); err != nil && err.Error() != "redis: nil" {
		// HSet 空集可能报错——直接确保键存在即可
		_ = p.client.Del(ctx, p.hashKey("empty.com"))
	}
	_ = mr

	hosts, err := p.ListHosts(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(hosts) != 2 {
		t.Fatalf("只应枚举两个凭证 host（借名键/空 hash 不算）: %+v", hosts)
	}
	if hosts[0].Host != "a.com" || hosts[0].Usernames != 1 || hosts[0].Sessions != 1 {
		t.Fatalf("a.com 分类: %+v", hosts[0])
	}
	if hosts[1].Host != "b.com" || hosts[1].Usernames != 1 || hosts[1].Sessions != 0 {
		t.Fatalf("b.com 分类: %+v", hosts[1])
	}
}
