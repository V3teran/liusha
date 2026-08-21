package web

import (
	"context"
	"testing"

	"github.com/V3teran/liusha/internal/executor"
)

// TestWebProfile_SatisfiesInterface 通过注册到 Registry 验证：加一个域只需实现接口 + 注册，
// 核心（Registry）零改动即可发现它——这是架构试金石的可执行断言。
func TestWebProfile_SatisfiesInterface(t *testing.T) {
	reg := executor.NewRegistry()
	reg.Register(New())

	got, ok := reg.Get("web")
	if !ok {
		t.Fatal("web Profile 注册后无法从 Registry 取回")
	}
	if got.Domain() != "web" {
		t.Fatalf("Domain() = %q, want web", got.Domain())
	}
	if len(reg.Domains()) != 1 {
		t.Fatalf("Domains() 应含 1 个域, got %v", reg.Domains())
	}
}

// TestWebProfile_Onboard 验证 Onboard 从自由文本抽取 web 目标——取代核心里的 briefHostRe。
// Locator = host[:port]，须与旧 briefHostRe 逐字一致（含端口、丢路径），保归档/限速键不漂。
func TestWebProfile_Onboard(t *testing.T) {
	p := New()
	refs, err := p.Onboard(context.Background(), executor.BriefInput{
		Brief: "测试 https://api.foo.com:8080/v1/login 和 http://bar.com 的注入",
	})
	if err != nil {
		t.Fatalf("Onboard 出错: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("应抽出 2 个目标, got %d: %v", len(refs), refs)
	}
	// 逐字保真：含端口的保端口，带路径的丢路径——与 briefHostRe 捕获组 [1] 一致。
	want := []string{"api.foo.com:8080", "bar.com"}
	for i, r := range refs {
		if r.Domain != "web" || r.RefKind != "host" {
			t.Errorf("TargetRef 域/种类错: %+v", r)
		}
		if r.Locator != want[i] {
			t.Errorf("Locator[%d] = %q, want %q", i, r.Locator, want[i])
		}
	}
}

// TestWebProfile_Onboard_Dedup 验证同一 host 多次出现只产一个目标（保序去重）。
func TestWebProfile_Onboard_Dedup(t *testing.T) {
	p := New()
	refs, err := p.Onboard(context.Background(), executor.BriefInput{
		Brief: "扫 https://foo.com/a 再扫 https://foo.com/b 和 http://bar.com",
	})
	if err != nil {
		t.Fatalf("Onboard 出错: %v", err)
	}
	if len(refs) != 2 || refs[0].Locator != "foo.com" || refs[1].Locator != "bar.com" {
		t.Fatalf("去重保序失败, got %+v", refs)
	}
}

// TestWebProfile_Onboard_NoTarget 验证无目标时明确报错（不静默返回空）。
func TestWebProfile_Onboard_NoTarget(t *testing.T) {
	p := New()
	if _, err := p.Onboard(context.Background(), executor.BriefInput{Brief: "没有任何 URL"}); err == nil {
		t.Fatal("brief 无 http 目标时应报错")
	}
}

// TestRegistry_Onboard 验证聚合 onboard：core 零分支，web 域自认领 http 目标。
func TestRegistry_Onboard(t *testing.T) {
	reg := executor.NewRegistry()
	reg.Register(New())

	refs, ok := reg.Onboard(context.Background(), executor.BriefInput{Brief: "扫 https://foo.com:443/x"})
	if !ok || len(refs) != 1 || refs[0].Locator != "foo.com:443" {
		t.Fatalf("聚合 onboard 失败, ok=%v refs=%+v", ok, refs)
	}

	// 无任何域能认领的 brief → 全域弃权，返回 false。
	if _, ok := reg.Onboard(context.Background(), executor.BriefInput{Brief: "没有 URL 的纯文本"}); ok {
		t.Error("无目标 brief 应返回 ok=false")
	}
}
