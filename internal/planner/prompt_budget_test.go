package planner

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/framework/runtime"
)

// section 造一段：标题加 n 条条目（每条 padBytes 字节的填充），条目编号 i 从旧到新。
func section(name string, n, padBytes int, newestFirst bool) promptSection {
	pad := strings.Repeat("字", padBytes/3+1)
	var sb strings.Builder
	sb.WriteString("## " + name + "\n")
	for i := 0; i < n; i++ {
		if newestFirst {
			fmt.Fprintf(&sb, "- %s 第 %d 条 %s\n", name, n-i, pad)
		} else {
			fmt.Fprintf(&sb, "- %s 第 %d 条 %s\n", name, i+1, pad)
		}
	}
	sb.WriteString("\n")
	return promptSection{name: name, newestFirst: newestFirst, text: sb.String()}
}

var foldLinePattern = regexp.MustCompile(`^- （更早的 (\d+) 条(.+)已折叠，需要时用 graph_read 深挖）$`)

// keptNumbers 取一段里逐条渲染的条目编号（按出现顺序）与折叠行上的数。
func keptNumbers(out, heading string) (numbers []int, folded int) {
	inSection := false
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "## ") {
			inSection = line == "## "+heading
			continue
		}
		if !inSection {
			continue
		}
		if m := foldLinePattern.FindStringSubmatch(line); m != nil {
			folded, _ = strconv.Atoi(m[1])
			continue
		}
		var n int
		if _, err := fmt.Sscanf(line, "- "+heading+" 第 %d 条", &n); err == nil {
			numbers = append(numbers, n)
		}
	}
	return numbers, folded
}

func TestFitPrompt_WithinBudgetIsVerbatim(t *testing.T) {
	sections := []promptSection{section("甲", 3, 30, false), {name: "", text: "固定的\n"}, section("乙", 2, 30, true)}
	if got, want := fitPrompt(sections, 10_000), concat(sections); got != want {
		t.Fatalf("预算内应原样输出\n得:\n%s\n期望:\n%s", got, want)
	}
}

// 超出预算：每一段都留最新的、折叠更早的，折叠行写明条数；不折叠的段一字不动；顺序不变。
func TestFitPrompt_FoldsOldestFirstAndKeepsFixedSections(t *testing.T) {
	fixed := promptSection{name: "", text: "## 待深挖的线索\n- [L1] 这一条必须看得见\n\n"}
	sections := []promptSection{section("甲", 40, 90, false), fixed, section("乙", 20, 90, false), section("丙", 4, 90, false)}
	budget := 4_000
	out := fitPrompt(sections, budget)

	if len(out) > budget {
		t.Fatalf("折叠后 %d 字节，超出预算 %d", len(out), budget)
	}
	if !strings.Contains(out, fixed.text) {
		t.Fatalf("不折叠的段应原样保留:\n%s", out)
	}
	if strings.Index(out, "## 甲") > strings.Index(out, "## 乙") || strings.Index(out, "## 乙") > strings.Index(out, "## 丙") {
		t.Fatalf("各段顺序应保持:\n%s", out)
	}
	for _, name := range []string{"甲", "乙"} {
		numbers, folded := keptNumbers(out, name)
		if len(numbers) < minKeepItems {
			t.Errorf("%s：至少留 %d 条，得 %d", name, minKeepItems, len(numbers))
		}
		// 旧的在前：留下的是编号最大的那一段，且连续
		for i, n := range numbers {
			if want := numbers[len(numbers)-1] - (len(numbers) - 1 - i); n != want {
				t.Errorf("%s：留下的应是最新且连续的条目，得 %v", name, numbers)
				break
			}
		}
		total := 40
		if name == "乙" {
			total = 20
		}
		if folded != total-len(numbers) {
			t.Errorf("%s：折叠行应写 %d 条，得 %d", name, total-len(numbers), folded)
		}
	}
	if numbers, folded := keptNumbers(out, "丙"); len(numbers) != 4 || folded != 0 {
		t.Errorf("不足最低保留数的组不动：得 %v，折叠 %d", numbers, folded)
	}
	// 等比：两段都按同一比例收紧（整条去掉，允许一条的误差）
	a, _ := keptNumbers(out, "甲")
	b, _ := keptNumbers(out, "乙")
	// 整条去掉（每条约一百字节、乙一共二十条），比例只能近似
	if ratioA, ratioB := float64(len(a))/40, float64(len(b))/20; ratioA > ratioB+0.12 || ratioA < ratioB-0.12 {
		t.Errorf("应按各段占用等比收紧：甲留 %.2f、乙留 %.2f", ratioA, ratioB)
	}
}

// 事实按类别分桶、桶内近者在前：留下的是开头的（新的），折叠行按类别称呼。
func TestFitPrompt_NewestFirstKeepsTheHead(t *testing.T) {
	text := "## 已确认的事实\n### 怎么反应（处理机制与限制）\n"
	for i := 80; i >= 1; i-- {
		text += fmt.Sprintf("- 反应事实 %d %s\n", i, strings.Repeat("字", 40))
	}
	text += "### 其他\n" + strings.Repeat("- 杂记\n", 3)
	sections := []promptSection{promptSection{name: "事实", newestFirst: true, text: text}, promptSection{name: "已完成动作", text: strings.Repeat("- 旧动作\n", 200)}}
	out := fitPrompt(sections, 6_000)

	if len(out) > 6_000 {
		t.Fatalf("折叠后 %d 字节", len(out))
	}
	if !strings.Contains(out, "- （更早的 ") || !strings.Contains(out, "条「怎么反应」事实已折叠") {
		t.Fatalf("事实的折叠行应按类别称呼:\n%s", out[:600])
	}
	var numbers []int
	for _, line := range strings.Split(out, "\n") {
		var n int
		if _, err := fmt.Sscanf(line, "- 反应事实 %d", &n); err == nil {
			numbers = append(numbers, n)
		}
	}
	if len(numbers) == 0 || numbers[0] != 80 {
		t.Fatalf("近者在前：留下的第一条应是最新的 80 号，得 %v", numbers)
	}
	for i, n := range numbers {
		if n != 80-i {
			t.Fatalf("留下的应是从最新起连续的条目，得 %v", numbers)
		}
	}
	if !strings.Contains(out, "### 其他\n- 杂记\n") {
		t.Fatalf("不到最低保留数的类别应整组保留:\n%s", out[len(out)-300:])
	}
}

// 各段自己写的折叠计数行不是条目：原样保留、不参与折叠。
func TestFitPrompt_PreservesBuilderFoldNotes(t *testing.T) {
	text := "## 已完成动作\n- （更早的 7 条已完成动作已折叠）\n"
	for i := 1; i <= 30; i++ {
		text += fmt.Sprintf("- 已完成动作 第 %d 条 %s\n", i, strings.Repeat("字", 60))
	}
	out := fitPrompt([]promptSection{{name: "已完成动作", text: text}}, 3_000)
	if !strings.Contains(out, "- （更早的 7 条已完成动作已折叠）") {
		t.Fatalf("各段自己的折叠行应保留:\n%s", out[:300])
	}
	numbers, folded := keptNumbers(out, "已完成动作")
	if folded != 30-len(numbers) || len(numbers) >= 30 {
		t.Fatalf("折叠行应只写本次折叠的条数：留 %d 条、折叠 %d", len(numbers), folded)
	}
}

// 不参与折叠的内容自己就超预算：截断并写明，不让规划者在发出去之前失败。
func TestFitPrompt_TruncatesWhenNothingIsFoldable(t *testing.T) {
	sections := []promptSection{{name: "", text: strings.Repeat("要处理的事\n", 2_000)}}
	out := fitPrompt(sections, 5_000)
	if len(out) > 5_000 || !strings.HasSuffix(out, "- （摘要超出预算，已截断）\n") {
		t.Fatalf("应截断到预算内并写明：%d 字节，结尾 %q", len(out), out[len(out)-40:])
	}
}

// 按实测任务收尾时的图面拼摘要：44 个动作、约 190 条事实、30 条线索、20 个结果。曾经拼出
// 约 10 万字节、越过单条消息上限；现在必须落在预算内，且待深挖的线索一条不少。
func TestBuildPlanningPrompt_StaysWithinBudgetOnAGrownGraph(t *testing.T) {
	repeat := func(n int) string { return strings.Repeat("探", n) }
	seq := 0
	node := func(kind core.NodeKind, state explorationgraph.State, content string) explorationgraph.Node {
		s := state
		seq++
		return explorationgraph.Node{
			ID: fmt.Sprintf("00000000-0000-4000-8000-%012d", seq), TaskID: "t1", Kind: kind,
			State: &s, SourceType: explorationgraph.SourcePlanner, Content: []byte(content),
		}
	}
	ctx := &PlanningContext{Objective: "测试网站 http://target.example/login.php，挖出尽可能多的漏洞。" + repeat(300)}
	for i := 0; i < 10; i++ {
		ctx.Directions = append(ctx.Directions, node(core.KindObjective, explorationgraph.StateOpen,
			fmt.Sprintf(`{"description":"方向 %d：%s"}`, i, repeat(140))))
	}
	for i := 0; i < 41; i++ {
		ctx.CompletedActions = append(ctx.CompletedActions, node(core.KindAction, explorationgraph.StateCompleted,
			fmt.Sprintf(`{"instruction":"目标 http://target.example，第 %d 步：%s"}`, i, repeat(200))))
	}
	for i := 0; i < 3; i++ {
		ctx.RejectedActions = append(ctx.RejectedActions, node(core.KindAction, explorationgraph.StateRejected,
			fmt.Sprintf(`{"instruction":"被否决的第 %d 步：%s"}`, i, repeat(200))))
	}
	for i := 0; i < 16; i++ {
		ctx.Results = append(ctx.Results, node(core.KindResult, explorationgraph.StateCompleted,
			fmt.Sprintf(`{"statement":"坐实结果 %d：%s"}`, i, repeat(260))))
	}
	for i := 0; i < 25; i++ {
		ctx.OpenLeads = append(ctx.OpenLeads, OpenLead{ID: fmt.Sprintf("lead-%d", i), Statement: fmt.Sprintf("线索 %d：%s", i, repeat(90))})
	}
	for i := 0; i < 16; i++ {
		ctx.ClosedLeads = append(ctx.ClosedLeads, ClosedLead{Statement: repeat(120), Kind: explorationgraph.ClosureNotWorth, Reason: repeat(200)})
	}
	for category, n := range map[explorationgraph.ObservationCategory]int{
		explorationgraph.CategoryBehavior: 80, explorationgraph.CategoryAccess: 27, explorationgraph.CategoryStructure: 14,
		explorationgraph.CategoryEntity: 33, explorationgraph.CategoryNote: 32,
	} {
		for i := 0; i < n; i++ {
			ctx.Facts = append(ctx.Facts, Fact{Category: category, Statement: fmt.Sprintf("%s 事实 %d %s", category, i, repeat(150))})
		}
	}
	for i := 0; i < 8; i++ {
		ctx.RefutedHypotheses = append(ctx.RefutedHypotheses, RefutedHypothesis{Statement: repeat(200), Reason: "claim_invalid", Detail: repeat(200)})
	}

	prompt := (&Engine{}).buildPlanningPrompt(ctx)
	if len(prompt) > planningPromptBudget {
		t.Fatalf("摘要 %d 字节，超出预算 %d", len(prompt), planningPromptBudget)
	}
	if planningPromptBudget >= runtime.MaxMessageBytes {
		t.Fatalf("预算 %d 必须低于单条消息上限 %d", planningPromptBudget, runtime.MaxMessageBytes)
	}
	if _, err := runtime.NewValidateModifier(runtime.MaxMessageBytes).Modify(t.Context(), []llm.Message{{Role: llm.RoleUser, Content: prompt}}); err != nil {
		t.Fatalf("摘要过不了框架的单条消息校验: %v", err)
	}
	// 这一轮要处理的事不折叠
	for i := 0; i < 25; i++ {
		if !strings.Contains(prompt, fmt.Sprintf("[L%d]", i+1)) {
			t.Fatalf("待深挖的线索 L%d 被折叠了", i+1)
		}
	}
	// 背景按比例留最新的
	if !strings.Contains(prompt, "已折叠，需要时用 graph_read 深挖") {
		t.Fatalf("超出预算时应折叠更早的背景\n%s", prompt[:800])
	}
	if strings.Contains(prompt, "第 0 步") && !strings.Contains(prompt, "第 40 步") {
		t.Fatalf("已完成的工作应留最新的、折叠更早的")
	}
	t.Logf("摘要 %d 字节（预算 %d，上限 %d）", len(prompt), planningPromptBudget, runtime.MaxMessageBytes)
}
