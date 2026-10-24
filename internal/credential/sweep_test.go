package credential

import (
	"context"
	"testing"
)

// fakeVault 是清扫器的内存 Provider 替身。
type fakeVault struct {
	byHost map[string][]Identity
}

func newFakeVault() *fakeVault { return &fakeVault{byHost: map[string][]Identity{}} }

func (f *fakeVault) SaveIdentities(_ context.Context, host string, ids []Identity) error {
	for _, id := range ids {
		replaced := false
		for i, ex := range f.byHost[host] {
			if ex.Username == id.Username {
				f.byHost[host][i] = id
				replaced = true
			}
		}
		if !replaced {
			f.byHost[host] = append(f.byHost[host], id)
		}
	}
	return nil
}

func (f *fakeVault) GetIdentitiesByHost(_ context.Context, host string) ([]Identity, error) {
	out := make([]Identity, len(f.byHost[host]))
	copy(out, f.byHost[host])
	return out, nil
}

func (f *fakeVault) DeleteIdentity(_ context.Context, host, name string) error {
	kept := f.byHost[host][:0]
	for _, id := range f.byHost[host] {
		if id.Username != name {
			kept = append(kept, id)
		}
	}
	f.byHost[host] = kept
	return nil
}

func (f *fakeVault) DeleteSessions(ctx context.Context, host string) (int, error) {
	n := 0
	for _, id := range f.byHost[host] {
		if IsSessionIdentity(id.Username) {
			if err := f.DeleteIdentity(ctx, host, id.Username); err == nil {
				n++
			}
		}
	}
	return n, nil
}

func (f *fakeVault) DeleteHost(_ context.Context, host string) error {
	delete(f.byHost, host)
	return nil
}

func (f *fakeVault) ListHosts(_ context.Context) ([]HostSummary, error) {
	var out []HostSummary
	for host, ids := range f.byHost {
		s := HostSummary{Host: host}
		for _, id := range ids {
			if IsSessionIdentity(id.Username) {
				s.Sessions++
			} else {
				s.Usernames++
			}
		}
		if s.Usernames+s.Sessions > 0 {
			out = append(out, s)
		}
	}
	return out, nil
}

// TestSweepTerminalSessions 锁定清扫语义：按任务状态这个事实回收——
// active（含 Reopen 复活中）保留，终态/任务行已删回收，持久身份与
// 全局会话不归清扫管。
func TestSweepTerminalSessions(t *testing.T) {
	ctx := context.Background()
	vault := newFakeVault()
	must := func(err error) {
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	must(vault.SaveIdentities(ctx, "a.com", []Identity{
		{Username: SessionIdentityName("t-active")},   // active：保留
		{Username: SessionIdentityName("t-done")},     // completed：回收
		{Username: SessionIdentityName("t-crashed")},  // 行已不存在（崩溃后级联删除）：回收
		{Username: SessionIdentityName("t-reopened")}, // active（复活）：保留
		{Username: "admin"},                           // 持久身份：不动
		{Username: "session"},                         // 全局会话：无归属，人工管理
	}))
	must(vault.SaveIdentities(ctx, "b.com", []Identity{
		{Username: SessionIdentityName("t-aborted")}, // aborted：回收
		{Username: "guest"},                          // 持久身份
	}))

	statuses := map[string]string{
		"t-active":   statusActive,
		"t-done":     statusCompleted,
		"t-reopened": statusActive,
		"t-aborted":  statusAborted,
	}
	lookup := func(_ context.Context, taskID string) (string, bool) {
		st, ok := statuses[taskID]
		return st, ok
	}

	removed, err := SweepTerminalSessions(ctx, vault, lookup)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if removed != 3 {
		t.Fatalf("应回收 3 个终态/已删任务的会话: %d", removed)
	}
	rest, _ := vault.GetIdentitiesByHost(ctx, "a.com")
	names := map[string]bool{}
	for _, id := range rest {
		names[id.Username] = true
	}
	for _, keep := range []string{"task:t-active:session", "task:t-reopened:session", "admin", "session"} {
		if !names[keep] {
			t.Fatalf("不应回收 %q（保留集: %+v）", keep, names)
		}
	}
	restB, _ := vault.GetIdentitiesByHost(ctx, "b.com")
	if len(restB) != 1 || restB[0].Username != "guest" {
		t.Fatalf("b.com 应只剩持久身份: %+v", restB)
	}
}
