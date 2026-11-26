package planner

import (
	"fmt"
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

// 待深挖的线索按陈述去重逐条渲染，带 ID（供动作 pursues_lead 认领）；没有时不渲染该节。
func TestBuildOpenLeadsSection(t *testing.T) {
	if got := buildOpenLeadsSection(nil); got != "" {
		t.Fatalf("没有待深挖线索时不应渲染，得:\n%s", got)
	}
	got := buildOpenLeadsSection([]OpenLead{
		{ID: "o1", Statement: "管理入口未鉴权"},
		{ID: "o2", Statement: "参数可注入"},
		{ID: "o3", Statement: "管理入口未鉴权"},
	})
	if !strings.Contains(got, "待深挖的线索") {
		t.Fatalf("应有小节标题，得:\n%s", got)
	}
	for _, want := range []string{"[o1]", "管理入口未鉴权", "[o2]", "参数可注入"} {
		if !strings.Contains(got, want) {
			t.Fatalf("应渲染线索 %q，得:\n%s", want, got)
		}
	}
	if strings.Count(got, "管理入口未鉴权") != 1 {
		t.Fatalf("同一陈述只渲染一次，得:\n%s", got)
	}
}

// 已判定不追的线索带理由逐条渲染；只增不减，超过上限时只留尾部、更早的折叠成计数行。
func TestBuildDeclinedLeadsSection(t *testing.T) {
	if got := buildDeclinedLeadsSection(nil); got != "" {
		t.Fatalf("没有已判定不追的线索时不应渲染，得:\n%s", got)
	}
	got := buildDeclinedLeadsSection([]DeclinedLead{{ID: "o1", Statement: "管理入口未鉴权", Reason: "超出任务目标"}})
	for _, want := range []string{"已判定不追的线索", "管理入口未鉴权", "理由：超出任务目标"} {
		if !strings.Contains(got, want) {
			t.Fatalf("应渲染 %q，得:\n%s", want, got)
		}
	}

	var many []DeclinedLead
	for i := 0; i < declinedLeadFoldLimit+5; i++ {
		many = append(many, DeclinedLead{ID: fmt.Sprintf("d%d", i), Statement: fmt.Sprintf("线索-%d", i), Reason: "前提已证伪"})
	}
	got = buildDeclinedLeadsSection(many)
	if strings.Count(got, "\n- 线索-") != declinedLeadFoldLimit {
		t.Fatalf("只逐条渲染最新的 %d 条，得 %d 行:\n%s", declinedLeadFoldLimit, strings.Count(got, "\n- 线索-"), got)
	}
	if !strings.Contains(got, fmt.Sprintf("（更早的 %d 条已判定不追的线索已折叠）", 5)) ||
		!strings.Contains(got, "\n- 线索-5（") || strings.Contains(got, "\n- 线索-4（") {
		t.Fatalf("折叠的应是最早的 5 条并留计数行，得:\n%s", got)
	}
}

// 对象清单类（entity / note）合计只留最新的若干条，另外三类不占名额、全量渲染：侦查期
// 涌入的大量对象类事实，不会把更早的入口、机制、关系类事实挤出视野。
func TestBuildFactsSection_InventoryCappedOthersKept(t *testing.T) {
	var facts []Fact
	for i := 0; i < inventoryRenderLimit+40; i++ {
		facts = append(facts, Fact{Category: explorationgraph.CategoryEntity, Statement: fmt.Sprintf("对象-%03d", i)})
	}
	// 创建序最早的三类事实：合计名额若按全局近优先分，它们最先被挤掉
	oldest := []Fact{
		{Category: explorationgraph.CategoryAccess, Statement: "入口甲可用身份 A 进入"},
		{Category: explorationgraph.CategoryBehavior, Statement: "输入过长时报错并回显"},
		{Category: explorationgraph.CategoryStructure, Statement: "甲经乙连到丙"},
	}
	facts = append(oldest, facts...)

	got := buildFactsSection(facts)
	for _, want := range oldest {
		if !strings.Contains(got, "\n- "+want.Statement+"\n") {
			t.Fatalf("%s 类事实被对象类事实挤出视野:\n%s", want.Category, got)
		}
	}
	if !strings.Contains(got, fmt.Sprintf("另有 %d 条较早的对象类事实未逐条列出", 40)) {
		t.Fatalf("超名额的对象类事实应折叠并留计数行，得:\n%s", got)
	}
	if !strings.Contains(got, "\n- 对象-040\n") || strings.Contains(got, "\n- 对象-039\n") {
		t.Fatalf("对象类应保留最新的 %d 条:\n%s", inventoryRenderLimit, got)
	}

	// 另外三类不设上限：数量远超对象类名额时仍逐条保留
	var many []Fact
	for i := 0; i < inventoryRenderLimit*3; i++ {
		many = append(many, Fact{Category: explorationgraph.CategoryAccess, Statement: fmt.Sprintf("条件-%03d", i)})
	}
	got = buildFactsSection(many)
	if strings.Count(got, "\n- 条件-") != inventoryRenderLimit*3 || strings.Contains(got, "未逐条列出") {
		t.Fatalf("入口 / 关系 / 机制类事实应全量渲染，得:\n%s", got)
	}
}

// 对象清单类的两个类别合起来按新旧取名额：一类的新事实再多，也不能把另一类更近的事实挤掉。
func TestBuildFactsSection_InventorySharesBudgetAcrossCategories(t *testing.T) {
	var facts []Fact
	// 创建序：40 条 note 在前，之后 40 条 entity。合计 80，应留最新的 60 条。
	for i := 0; i < 40; i++ {
		facts = append(facts, Fact{Category: explorationgraph.CategoryNote, Statement: fmt.Sprintf("杂项-%03d", i)})
	}
	for i := 0; i < 40; i++ {
		facts = append(facts, Fact{Category: explorationgraph.CategoryEntity, Statement: fmt.Sprintf("对象-%03d", i)})
	}

	got := buildFactsSection(facts)
	if !strings.Contains(got, "\n- 杂项-020\n") || strings.Contains(got, "\n- 杂项-019\n") {
		t.Fatalf("应保留最新的 20 条杂项（两类合计 %d），得:\n%s", inventoryRenderLimit, got)
	}
	if !strings.Contains(got, "\n- 对象-000\n") || !strings.Contains(got, "\n- 对象-039\n") {
		t.Fatalf("40 条对象类都在最新的 %d 条之内，应全部保留，得:\n%s", inventoryRenderLimit, got)
	}
	if !strings.Contains(got, "另有 20 条较早的对象类事实未逐条列出") {
		t.Fatalf("应折叠最早的 20 条并留计数行，得:\n%s", got)
	}
}

// 去重按陈述全文：开头相同、后半不同的两条事实，截断后文本一样，也各占一行；陈述完全
// 相同的只留一条。按截断后的文本去重会把前者错并成一条，被并掉的事实就此消失。
func TestBuildFactsSection_DedupsByFullStatement(t *testing.T) {
	prefix := strings.Repeat("长", factClampRunes)
	same := Fact{Category: explorationgraph.CategoryAccess, Statement: "入口可达"}
	got := buildFactsSection([]Fact{
		{Category: explorationgraph.CategoryBehavior, Statement: prefix + "：结论甲"},
		same,
		{Category: explorationgraph.CategoryBehavior, Statement: prefix + "：结论乙"},
		same,
	})
	if n := strings.Count(got, "\n- "+prefix); n != 2 {
		t.Fatalf("开头相同的两条事实应各占一行，得 %d 行:\n%s", n, got)
	}
	if strings.Count(got, "\n- 入口可达\n") != 1 {
		t.Fatalf("陈述完全相同的事实只留一条，得:\n%s", got)
	}
}
