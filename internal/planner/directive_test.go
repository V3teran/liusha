package planner

import (
	"strings"
	"testing"

	"github.com/V3teran/liusha/internal/worldmodel"
)

func TestDirective_ZeroValueEmpty(t *testing.T) {
	// 零值 Move（降级路径：无世界模型时直跑）不注入任何指向。
	if got := (Move{}).Directive(); got != "" {
		t.Fatalf("零值 Move.Directive() 应为空串，得: %q", got)
	}
}

func TestDirective_RendersStageAndAnchor(t *testing.T) {
	in := Move{
		Kind:   MoveExploit,
		Target: worldmodel.TargetRef{Domain: "web", RefKind: "endpoint", Locator: "/login"},
		Reason: "资产尚未验证漏洞",
	}
	got := in.Directive()
	for _, want := range []string{"exploit", "/login", "web/endpoint", "资产尚未验证漏洞", "坐实可利用的缺陷"} {
		if !strings.Contains(got, want) {
			t.Errorf("Directive() 缺少 %q，全文:\n%s", want, got)
		}
	}
}

func TestDirective_UnknownKindStillRendersHeader(t *testing.T) {
	// Kind 非空但无内置指向文案：仍渲染阶段行，不 panic、不吞。
	in := Move{Kind: MoveKind("lateral-move"), Reason: "x"}
	got := in.Directive()
	if !strings.Contains(got, "lateral-move") {
		t.Errorf("未知 Kind 应仍渲染阶段行，得:\n%s", got)
	}
}

func TestDirective_OmitsEmptyOptionalLines(t *testing.T) {
	// 无 Locator / 无 Reason 时不产出空的锚点/因由行。
	in := Move{Kind: MoveEnumerate}
	got := in.Directive()
	if strings.Contains(got, "锚点") {
		t.Errorf("无 Locator 不应出现锚点行，得:\n%s", got)
	}
	if strings.Contains(got, "因由") {
		t.Errorf("无 Reason 不应出现因由行，得:\n%s", got)
	}
}
