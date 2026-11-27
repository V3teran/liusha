package planner

import (
	"fmt"
	"strings"
	"testing"
	"time"

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

	pending := buildPendingActionsSection(&PlanningContext{PendingActions: []explorationgraph.Node{assigned, planned}})
	if !strings.Contains(pending, "[A1] [进行中·用户指派] [ID: a1]") || !strings.Contains(pending, "[A2] [进行中] [ID: a2]") {
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

// 场景块拼在常规规划与结果分析两份系统提示词末尾；无场景时与原来逐字一致。
func TestPlannerSystemPrompts_AppendScenario(t *testing.T) {
	plain := NewEngine(nil, zerolog.Nop()).WithCharter("章程")
	block := "<scenario name=\"渗透测试\">\n正文\n</scenario>"
	withScenario := NewEngine(nil, zerolog.Nop()).WithCharter("章程").WithScenario(block)
	for name, pair := range map[string][2]string{
		"规划": {plain.buildPlannerSystemPrompt(), withScenario.buildPlannerSystemPrompt()},
		"分析": {plain.buildAnalysisSystemPrompt(), withScenario.buildAnalysisSystemPrompt()},
	} {
		if pair[1] != pair[0]+"\n\n"+block {
			t.Errorf("%s：场景块应拼在末尾:\n%s", name, pair[1])
		}
		if strings.Contains(pair[0], "<scenario") {
			t.Errorf("%s：无场景时不应有场景块", name)
		}
	}
}

// 已证伪假设渲染时带上评估者的理由；没有理由（机器判出）时只有原因说明。
func TestBuildRefutedHypothesesSection_RendersDetail(t *testing.T) {
	got := buildRefutedHypothesesSection([]RefutedHypothesis{
		{Statement: "主张甲", Reason: evaluator.RefuteNotReproducible, Detail: "只读到本人记录"},
		{Statement: "主张乙", Reason: evaluator.RefuteInvalidRecipe},
	})
	if !strings.Contains(got, "主张甲") || !strings.Contains(got, "评估说明：只读到本人记录") {
		t.Fatalf("应渲染评估者理由，得:\n%s", got)
	}
	if strings.Count(got, "评估说明") != 1 {
		t.Fatalf("没有理由的假设不应渲染评估说明，得:\n%s", got)
	}
}

// 待验证的假设按陈述去重逐条渲染；没有时不渲染该节。
func TestBuildPendingHypothesesSection(t *testing.T) {
	if got := buildPendingHypothesesSection(nil); got != "" {
		t.Fatalf("没有待验证假设时不应渲染，得:\n%s", got)
	}
	got := buildPendingHypothesesSection([]string{"主张甲", "主张乙", "主张甲"})
	if !strings.Contains(got, "待验证") || !strings.Contains(got, "主张甲") || !strings.Contains(got, "主张乙") {
		t.Fatalf("应逐条渲染待验证假设，得:\n%s", got)
	}
	if strings.Count(got, "主张甲") != 1 {
		t.Fatalf("同一陈述只渲染一次，得:\n%s", got)
	}
}

// 进行中的工作每条带短号（线索可以声明由它覆盖）。
func TestBuildPendingActionsSection_MarksHandles(t *testing.T) {
	open := explorationgraph.StateOpen
	ctx := &PlanningContext{PendingActions: []explorationgraph.Node{{ID: "a1", State: &open}, {ID: "a2", State: &open}}}
	got := buildPendingActionsSection(ctx)
	for _, want := range []string{"[A1] [进行中] [ID: a1]", "[A2] [进行中] [ID: a2]"} {
		if !strings.Contains(got, want) {
			t.Fatalf("在办动作应带短号，缺 %q:\n%s", want, got)
		}
	}
	if buildPendingActionsSection(&PlanningContext{}) != "" {
		t.Fatal("没有在办动作时不应渲染")
	}
}

// 深挖与铺开对象面各用了多少动作、各有多少在办，如实摆出来：规划者据此看自己的深浅比例。
func TestBuildStatisticsSection_SplitsFollowingAndExploring(t *testing.T) {
	ctx := &PlanningContext{
		CompletedActions: []explorationgraph.Node{{ID: "f1"}, {ID: "e1"}},
		FailedActions:    []explorationgraph.Node{{ID: "e2"}},
		PendingActions:   []explorationgraph.Node{{ID: "p1"}, {ID: "p2"}},
		claiming:         map[string]bool{"f1": true, "p1": true},
	}
	got := buildStatisticsSection(ctx)
	for _, want := range []string{"- 已完成动作: 2", "- 进行中动作: 2", "跟进线索的动作（深挖）: 2（在办 1）",
		"不追线索的动作（铺开对象面）: 3（在办 1）"} {
		if !strings.Contains(got, want) {
			t.Fatalf("应渲染 %q，得:\n%s", want, got)
		}
	}
}

// 待深挖的线索逐条渲染、带短号（供动作认领）：同一陈述的并成一行、短号都列出，没有哪条
// 线索是看不见的；每条带它由图上推导的处境。没有时不渲染该节。
func TestBuildOpenLeadsSection(t *testing.T) {
	if got := buildOpenLeadsSection(&PlanningContext{}); got != "" {
		t.Fatalf("没有待深挖线索时不应渲染，得:\n%s", got)
	}
	now := time.Unix(1700000100, 0)
	ctx := &PlanningContext{
		PendingActions: []explorationgraph.Node{{ID: "producer"}},
		Now:            now,
		OpenLeads: []OpenLead{
			{ID: "o1", Statement: "管理入口未鉴权", ProducerID: "producer", CreatedAt: now.Add(-12 * time.Minute)},
			{ID: "o2", Statement: "参数可注入", Truncated: 2, TruncationStreak: 2, Vetoes: 1, LastVetoReason: "重复",
				CreatedAt: now.Add(-90 * time.Minute)},
			{ID: "o3", Statement: "管理入口未鉴权", ProducerID: "producer", CreatedAt: now.Add(-12 * time.Minute)},
			{ID: "o4", Statement: "另一条路", Progressed: 1, Faults: 1, CreatedAt: now.Add(-30 * time.Second)},
		},
	}
	got := buildOpenLeadsSection(ctx)
	if !strings.Contains(got, "待深挖的线索") {
		t.Fatalf("应有小节标题，得:\n%s", got)
	}
	for _, want := range []string{
		"- [L1、L3] 管理入口未鉴权（由在办的 A1 产出；已挂 12 分钟）\n",
		"- [L2] 参数可注入（已挂 1 小时 30 分钟；没做完 2 次；连续没做完 2 次；被否决 1 次：重复）\n",
		"- [L4] 另一条路（已挂不到 1 分钟；执行故障 1 次；跟进过 1 次、推进了但没到结论）\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("应渲染 %q，得:\n%s", want, got)
		}
	}
	if strings.Count(got, "管理入口未鉴权") != 1 {
		t.Fatalf("同一陈述只占一行，得:\n%s", got)
	}

	// 同一行上的线索处境不同：逐条标出短号。
	ctx.OpenLeads[2].ProducerID = ""
	got = buildOpenLeadsSection(ctx)
	if !strings.Contains(got, "- [L1、L3] 管理入口未鉴权（L1：由在办的 A1 产出；已挂 12 分钟；L3：已挂 12 分钟）\n") {
		t.Fatalf("处境不同时逐条标出，得:\n%s", got)
	}
}

// 已关闭的线索带关闭原因逐条渲染；只增不减，超过上限时只留尾部、更早的折叠成计数行。
func TestBuildClosedLeadsSection(t *testing.T) {
	if got := buildClosedLeadsSection(nil); got != "" {
		t.Fatalf("没有已关闭的线索时不应渲染，得:\n%s", got)
	}
	got := buildClosedLeadsSection([]ClosedLead{
		{Statement: "管理入口未鉴权", Kind: explorationgraph.ClosureOutOfScope, Reason: "不在授权的目标范围内"},
		{Statement: "配置泄露", Kind: explorationgraph.ClosureDry},
		{Statement: "旧入口", Kind: explorationgraph.ClosureBlocked, Reason: "需要管理员凭据"},
	})
	for _, want := range []string{"已关闭的线索", "管理入口未鉴权（超出任务目标：不在授权的目标范围内）",
		"配置泄露（干涸）", "旧入口（受阻：需要管理员凭据）"} {
		if !strings.Contains(got, want) {
			t.Fatalf("应渲染 %q，得:\n%s", want, got)
		}
	}

	var many []ClosedLead
	for i := 0; i < closedLeadFoldLimit+5; i++ {
		many = append(many, ClosedLead{Statement: fmt.Sprintf("线索-%d", i), Kind: explorationgraph.ClosureConcluded})
	}
	got = buildClosedLeadsSection(many)
	if strings.Count(got, "\n- 线索-") != closedLeadFoldLimit {
		t.Fatalf("只逐条渲染最新的 %d 条，得 %d 行:\n%s", closedLeadFoldLimit, strings.Count(got, "\n- 线索-"), got)
	}
	if !strings.Contains(got, fmt.Sprintf("（更早的 %d 条已关闭的线索已折叠）", 5)) ||
		!strings.Contains(got, "\n- 线索-5（") || strings.Contains(got, "\n- 线索-4（") {
		t.Fatalf("折叠的应是最早的 5 条并留计数行，得:\n%s", got)
	}
}

// 进行中的派生目标逐条列出，供规划者分组与取舍。
func TestBuildObjectiveSection_listsLiveObjectives(t *testing.T) {
	got := buildObjectiveSection("摸清目标", []explorationgraph.Node{
		{ID: "d1", Kind: core.KindObjective, Content: []byte(`{"description":"摸清配置面"}`)},
		{ID: "d2", Kind: core.KindObjective, Content: []byte(`{"description":"验证注入"}`)},
	})
	if !strings.Contains(got, "## 任务目标\n摸清目标\n\n### 进行中的方向\n- 摸清配置面\n- 验证注入\n") {
		t.Fatalf("进行中的派生目标逐条列出，得:\n%s", got)
	}
	if got := buildObjectiveSection("摸清目标", nil); strings.Contains(got, "进行中的方向") {
		t.Fatalf("没有进行中的方向时不出现该小节，得:\n%s", got)
	}
}
