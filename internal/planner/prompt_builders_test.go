package planner

import (
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/evaluator"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
	plannercontract "github.com/V3teran/liusha/internal/planner/contract"
)

// TestRefuteReasonHint_EveryReasonHasExplicitMeaning：evaluator 产出的每个反驳原因都要有
// 专属的含义说明，且带上原因字面量（章程按字面量写处置）。落到兜底文案会抹掉原因的语义——
// judge_unavailable 的定义是「评估环节没跑通、主张未被检验」，曾因缺分支被当成已证伪。
func TestRefuteReasonHint_EveryReasonHasExplicitMeaning(t *testing.T) {
	fallback := refuteReasonHint("__unknown__")
	for _, reason := range []string{
		evaluator.RefuteNotReproducible,
		evaluator.RefuteClaimInvalid,
		evaluator.RefuteAssertNotDiscriminative,
		evaluator.RefuteInvalidRecipe,
		evaluator.RefuteJudgeUnavailable,
	} {
		got := refuteReasonHint(reason)
		if got == fallback || !strings.Contains(got, reason) {
			t.Errorf("反驳原因 %q 应有带字面量的专属说明，得 %q", reason, got)
		}
	}
	if hint := refuteReasonHint(evaluator.RefuteJudgeUnavailable); !strings.Contains(hint, "未被检验") {
		t.Errorf("judge_unavailable 应说明主张未被检验，得 %q", hint)
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

// 已否决的动作：规划者看到否决方、原因与「判定不该做」的状态含义（不得等效重提的纪律在章程）。
func TestBuildRejectedActionsSection_NamesRejecterAndMeaning(t *testing.T) {
	rejected := explorationgraph.StateRejected
	reason := "超出授权范围"
	section := buildRejectedActionsSection([]explorationgraph.Node{{
		ID: "a1", Kind: core.KindAction, State: &rejected, StateReason: &reason,
		Content: []byte(`{"instruction":"扫描 C 段"}`), Metadata: []byte(`{"rejected_by":"user"}`),
	}})
	for _, want := range []string{"不该做", "扫描 C 段", reason, "用户"} {
		if !strings.Contains(section, want) {
			t.Fatalf("缺少 %q:\n%s", want, section)
		}
	}
}

// 已中止的动作没有对错判断：规划者看到中止原因与状态含义（可按当前目标重提的纪律在章程）。
func TestBuildAbortedActionsSection_NoJudgement(t *testing.T) {
	aborted := explorationgraph.StateAborted
	reason := "目标已调整，撤回未执行的规划"
	section := buildAbortedActionsSection([]explorationgraph.Node{{
		ID: "a1", Kind: core.KindAction, State: &aborted, StateReason: &reason,
		Content: []byte(`{"instruction":"探测登录接口"}`),
	}})
	if !strings.Contains(section, reason) || !strings.Contains(section, "不含对错判断") {
		t.Fatalf("应展示中止原因与状态含义:\n%s", section)
	}
	if strings.Contains(section, "禁止") {
		t.Fatalf("中止不含对错判断，不应禁止重提:\n%s", section)
	}
}

// 重规划理由逐条进入规划提示；不是重规划时不渲染该节。
func TestBuildReplanSection_ListsReasons(t *testing.T) {
	if got := buildReplanSection(nil); got != "" {
		t.Fatalf("无理由不应渲染：%q", got)
	}
	section := buildReplanSection([]string{"探索停滞", "任务目标已调整"})
	for _, want := range []string{"重规划", "探索停滞", "任务目标已调整"} {
		if !strings.Contains(section, want) {
			t.Fatalf("缺少 %q:\n%s", want, section)
		}
	}
}

// 用户指派的动作在规划视野里标出来源：规划者知道哪些是用户直接要求的，不重复提出等效动作。
func TestActionSections_LabelUserAssigned(t *testing.T) {
	open := explorationgraph.StateOpen
	done := explorationgraph.StateCompleted
	assigned := explorationgraph.Node{ID: "a1", Kind: core.KindAction, State: &open,
		SourceType: explorationgraph.SourceUser, Content: []byte(`{"instruction":"复测登录接口"}`)}
	planned := explorationgraph.Node{ID: "a2", Kind: core.KindAction, State: &open,
		SourceType: explorationgraph.SourcePlanner, Content: []byte(`{"instruction":"枚举目录"}`)}

	pending := buildPendingActionsSection([]explorationgraph.Node{assigned, planned})
	if !strings.Contains(pending, "[进行中·用户指派] [ID: a1]") || !strings.Contains(pending, "[进行中] [ID: a2]") {
		t.Fatalf("进行中的用户指派动作应标出来源:\n%s", pending)
	}
	assigned.State = &done
	completed := buildCompletedActionsSection([]explorationgraph.Node{assigned})
	if !strings.Contains(completed, "[完成·用户指派] [ID: a1]") {
		t.Fatalf("已完成的用户指派动作应标出来源:\n%s", completed)
	}
}

// 结果分析是规划者的另一项职责：system prompt 由规划者章程（可自定义）+ 结果分析契约组成，
// 方法论只在章程里；章程为空时不发起调用。
func TestBuildAnalysisSystemPrompt(t *testing.T) {
	e := NewEngine(nil, zerolog.Nop()).WithCharter("规划者章程正文")
	got := e.buildAnalysisSystemPrompt()
	if !strings.HasPrefix(got, "规划者章程正文") || !strings.Contains(got, plannercontract.AnalysisText) {
		t.Fatalf("应为章程 + 结果分析契约:\n%s", got)
	}
	if (&Engine{}).buildAnalysisSystemPrompt() != "" {
		t.Fatal("章程为空时应返回空串")
	}
}
