package planner

import (
	"strings"
	"testing"

	"github.com/V3teran/liusha/internal/evaluator"
)

// TestRefuteReasonHint_EveryReasonHasExplicitGuidance：evaluator 产出的每个反驳原因都要有
// 专属规划指令。落到兜底文案（「不要原样重提」）会与原因本身的语义冲突——
// adjudication_failed 的定义是「裁决环节没跑通、主张未被否定、方向可重试」，曾因缺分支
// 被告知 planner 不要重提。
func TestRefuteReasonHint_EveryReasonHasExplicitGuidance(t *testing.T) {
	fallback := refuteReasonHint("__unknown__")
	for _, reason := range []string{
		evaluator.RefuteNotReproducible,
		evaluator.RefuteClaimInvalid,
		evaluator.RefuteAssertNotDiscriminative,
		evaluator.RefuteInvalidRecipe,
		evaluator.RefuteAdjudicationFailed,
	} {
		if got := refuteReasonHint(reason); got == fallback {
			t.Errorf("反驳原因 %q 落到兜底指令 %q", reason, got)
		}
	}
	if hint := refuteReasonHint(evaluator.RefuteAdjudicationFailed); strings.Contains(hint, "不要") || strings.Contains(hint, "作废") {
		t.Errorf("adjudication_failed 应指示可按原方向重试，得 %q", hint)
	}
}
