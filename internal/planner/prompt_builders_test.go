package planner

import (
	"strings"
	"testing"

	"github.com/V3teran/liusha/internal/evaluator"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
)

// TestRefuteReasonHint_EveryReasonHasExplicitGuidance：evaluator 产出的每个反驳原因都要有
// 专属规划指令。落到兜底文案（「不要原样重提」）会与原因本身的语义冲突——
// judge_unavailable 的定义是「评估环节没跑通、主张未被否定、方向可重试」，曾因缺分支
// 被告知 planner 不要重提。
func TestRefuteReasonHint_EveryReasonHasExplicitGuidance(t *testing.T) {
	fallback := refuteReasonHint("__unknown__")
	for _, reason := range []string{
		evaluator.RefuteNotReproducible,
		evaluator.RefuteClaimInvalid,
		evaluator.RefuteAssertNotDiscriminative,
		evaluator.RefuteInvalidRecipe,
		evaluator.RefuteJudgeUnavailable,
	} {
		if got := refuteReasonHint(reason); got == fallback {
			t.Errorf("反驳原因 %q 落到兜底指令 %q", reason, got)
		}
	}
	if hint := refuteReasonHint(evaluator.RefuteJudgeUnavailable); strings.Contains(hint, "不要") || strings.Contains(hint, "作废") {
		t.Errorf("judge_unavailable 应指示可按原方向重试，得 %q", hint)
	}
}

// 失败动作展示的是失败原因（状态原因），不是当初的规划理由——规划者据此判断此路
// 是否走得通。
func TestBuildFailedActionsSection_ShowsFailureNotRationale(t *testing.T) {
	failed := explorationgraph.StateFailed
	reason := "工具超时：sqlmap 运行超过 120 秒"
	section := buildFailedActionsSection([]explorationgraph.Node{{
		ID: "a1", Kind: core.KindAction, State: &failed, StateReason: &reason,
		Content: []byte(`{"instruction":"跑 sqlmap","reasoning":"登录表单疑似注入"}`),
	}})
	if !strings.Contains(section, reason) {
		t.Fatalf("应展示失败原因:\n%s", section)
	}
	if strings.Contains(section, "登录表单疑似注入") {
		t.Fatalf("不应把规划理由当失败原因:\n%s", section)
	}
}

// 已否决的动作：规划者看到否决方与原因，并被告知禁止等效重提。
func TestBuildRejectedActionsSection_ForbidsEquivalentAndNamesRejecter(t *testing.T) {
	rejected := explorationgraph.StateRejected
	reason := "超出授权范围"
	section := buildRejectedActionsSection([]explorationgraph.Node{{
		ID: "a1", Kind: core.KindAction, State: &rejected, StateReason: &reason,
		Content: []byte(`{"instruction":"扫描 C 段"}`), Metadata: []byte(`{"rejected_by":"user"}`),
	}})
	for _, want := range []string{"禁止等效重提", "扫描 C 段", reason, "用户"} {
		if !strings.Contains(section, want) {
			t.Fatalf("缺少 %q:\n%s", want, section)
		}
	}
}

// 已中止的动作没有对错判断：规划者看到中止原因，并被告知与当前目标相关的可以重新提出。
func TestBuildAbortedActionsSection_AllowsReproposal(t *testing.T) {
	aborted := explorationgraph.StateAborted
	reason := "目标已调整，撤回未执行的规划"
	section := buildAbortedActionsSection([]explorationgraph.Node{{
		ID: "a1", Kind: core.KindAction, State: &aborted, StateReason: &reason,
		Content: []byte(`{"instruction":"探测登录接口"}`),
	}})
	if !strings.Contains(section, reason) || !strings.Contains(section, "可以重新提出") {
		t.Fatalf("应展示中止原因并允许重提:\n%s", section)
	}
	if strings.Contains(section, "禁止") {
		t.Fatalf("中止不含对错判断，不应禁止重提:\n%s", section)
	}
}
