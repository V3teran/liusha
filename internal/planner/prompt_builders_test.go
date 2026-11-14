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
		ID: "a1", Kind: core.KindAction, State: &failed, BlockedReason: &reason,
		Content: []byte(`{"instruction":"跑 sqlmap","reasoning":"登录表单疑似注入"}`),
	}})
	if !strings.Contains(section, reason) {
		t.Fatalf("应展示失败原因:\n%s", section)
	}
	if strings.Contains(section, "登录表单疑似注入") {
		t.Fatalf("不应把规划理由当失败原因:\n%s", section)
	}
}
