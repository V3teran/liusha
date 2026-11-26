package planner

import (
	"github.com/V3teran/liusha/internal/evaluator"

	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/textx"
)

// buildTeamRosterSection 构建多智能体分工名册：四角色的展示名 + 调度摘要。
// 调度摘要来自各 agent.description（前端「智能体」页可编辑）。名册为空时不渲染该节。
func buildTeamRosterSection(roster []AgentBrief) string {
	if len(roster) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("## 多智能体分工\n")
	for _, a := range roster {
		if a.Description == "" {
			fmt.Fprintf(&sb, "- %s（%s）\n", a.Code, a.Name)
			continue
		}
		fmt.Fprintf(&sb, "- %s（%s）：%s\n", a.Code, a.Name, a.Description)
	}
	sb.WriteString("\n")
	return sb.String()
}

// buildObjectiveSection 构建任务目标部分：根目标即任务目标本体，进行中的派生
// 目标逐条列出。已收敛的派生目标不再列出——已达成的无需再投入，已吸收的范围
// 由吸收它的新目标承接（新目标本身在列）。
func buildObjectiveSection(objective string, objectives []explorationgraph.Node) string {
	var sb strings.Builder
	sb.WriteString("## 任务目标\n")
	if objective != "" {
		sb.WriteString(objective)
	} else {
		sb.WriteString("（未指定明确目标）")
	}
	sb.WriteString("\n\n")

	if live := newObjectiveView(objectives).live; len(live) > 0 {
		sb.WriteString("### 进行中的派生目标\n")
		for _, obj := range live {
			fmt.Fprintf(&sb, "- %s\n", objectiveDescription(obj))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// buildReplanSection 构建重规划理由部分：本轮是被请求的重规划（监察者纠偏、任务目标
// 调整等），理由逐条列出。常规规划返回空串。
func buildReplanSection(reasons []string) string {
	if len(reasons) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("## 本轮是重规划，理由\n")
	for _, r := range reasons {
		fmt.Fprintf(&sb, "- %s\n", r)
	}
	sb.WriteString("\n")
	return sb.String()
}

// buildStatisticsSection 构建统计信息部分
func buildStatisticsSection(ctx *PlanningContext) string {
	var sb strings.Builder
	sb.WriteString("## 当前探索统计\n")
	fmt.Fprintf(&sb, "- 已完成动作: %d\n", len(ctx.CompletedActions))
	fmt.Fprintf(&sb, "- 进行中动作: %d\n", len(ctx.PendingActions))
	fmt.Fprintf(&sb, "- 已坐实结果: %d\n", len(ctx.Results))
	sb.WriteString("\n")
	return sb.String()
}

// ── 渲染预算 ─────────────────────────────────────────────────────────
// 规划 prompt 是 planner ReAct 的首条输入，其体量必须有界：探索图只增不减，
// 长任务下全量渲染会无界增长直至撞上长度校验哨兵弄死整轮规划。按用途分层——
// pending 动作、已确认发现与待深挖的线索全量（规划的直接输入），终态动作与已判定
// 不追的线索只逐条渲染尾部、更早的折叠成计数行，事实按类别分配名额（见
// buildFactsSection）。确定性渲染，无 LLM 参与、不可能失败；折叠处以显式文字留痕
// （不静默）。

const (
	// actionFoldLimit 是终态动作（completed/failed/aborted）逐条渲染的上限。
	actionFoldLimit = 30
	// declinedLeadFoldLimit 是已判定不追的线索逐条渲染的上限。
	declinedLeadFoldLimit = 30
	// inventoryRenderLimit 是对象清单类事实（entity / note）合计逐条渲染的上限，只留
	// 最新的这些；其余三类不占这个名额，全量渲染（见 buildFactsSection）。
	inventoryRenderLimit = 60
	// prompt 内嵌文本的钳长（rune）。
	actionClampRunes  = 300
	resultClampRunes  = 600
	factClampRunes    = 400
	reasonClampRunes  = 200
	historyClampRunes = 600
)

// foldTail 把按时间先后排列的序列拆成「逐条渲染的尾部」与被折叠的更早条数。
func foldTail[T any](items []T, limit int) (shown []T, foldedTotal int) {
	if len(items) <= limit {
		return items, 0
	}
	cut := len(items) - limit
	return items[cut:], cut
}

// foldTerminalActions 把终态动作序列拆成「逐条渲染的尾部」与被折叠的更早条数。
func foldTerminalActions(actions []explorationgraph.Node) (shown []explorationgraph.Node, foldedTotal int) {
	return foldTail(actions, actionFoldLimit)
}

// foldLine 渲染折叠计数行。
func foldLine(label string, total int) string {
	if total == 0 {
		return ""
	}
	return fmt.Sprintf("- （更早的 %d 条%s已折叠）\n", total, label)
}

// actionTag 是动作行的状态标记，用户指派的动作附上来源（规划者据此知道哪些是用户直接要求的）。
func actionTag(state string, n explorationgraph.Node) string {
	if n.SourceType == explorationgraph.SourceUser {
		return "[" + state + "·用户指派]"
	}
	return "[" + state + "]"
}

// actionInstruction 读动作指令并钳长。
func actionInstruction(n explorationgraph.Node) string {
	var a struct {
		Instruction string `json:"instruction"`
	}
	if err := json.Unmarshal(n.Content, &a); err != nil {
		return "（解析失败）"
	}
	return textx.TruncateRunes(a.Instruction, actionClampRunes, "…（截断）")
}

// buildCompletedActionsSection 构建已完成工作部分（尾部逐条 + 更早折叠）。
func buildCompletedActionsSection(actions []explorationgraph.Node) string {
	var sb strings.Builder
	sb.WriteString("## 已完成的工作\n")
	if len(actions) == 0 {
		sb.WriteString("（尚未完成任何操作）\n\n")
		return sb.String()
	}
	shown, total := foldTerminalActions(actions)
	sb.WriteString(foldLine("已完成动作", total))
	for _, action := range shown {
		fmt.Fprintf(&sb, "- %s [ID: %s] %s\n", actionTag("完成", action), action.ID, actionInstruction(action))
	}
	sb.WriteString("\n")
	return sb.String()
}

// buildPendingActionsSection 构建进行中工作部分（规划的直接输入，全量逐条）。
func buildPendingActionsSection(actions []explorationgraph.Node) string {
	if len(actions) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## 进行中的工作\n")
	for _, action := range actions {
		fmt.Fprintf(&sb, "- %s [ID: %s] %s\n", actionTag("进行中", action), action.ID, actionInstruction(action))
	}
	sb.WriteString("\n")
	return sb.String()
}

// buildFailedActionsSection 构建失败尝试部分（尾部逐条 + 更早折叠）。
func buildFailedActionsSection(actions []explorationgraph.Node) string {
	if len(actions) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## 失败的尝试\n")
	shown, total := foldTerminalActions(actions)
	sb.WriteString(foldLine("失败动作", total))
	for _, action := range shown {
		fmt.Fprintf(&sb, "- %s [ID: %s] %s（原因：%s）\n", actionTag("失败", action), action.ID, actionInstruction(action), stateReason(action, "未知原因"))
	}
	sb.WriteString("\n")
	return sb.String()
}

// buildRejectedActionsSection 构建已否决部分：监察者或用户判定不该做的动作，连同否决方
// 与原因呈现，规划者据此不再派等效方向（否则否决的反馈回路断一半）。尾部逐条 + 更早折叠。
func buildRejectedActionsSection(actions []explorationgraph.Node) string {
	if len(actions) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## 已否决的动作（监察者或用户判定这一步不该做）\n")
	shown, total := foldTerminalActions(actions)
	sb.WriteString(foldLine("已否决动作", total))
	for _, action := range shown {
		fmt.Fprintf(&sb, "- [已否决·%s] [ID: %s] %s（理由：%s）\n",
			rejecterLabel(action.RejectedBy()), action.ID, actionInstruction(action), stateReason(action, "未注明"))
	}
	sb.WriteString("\n")
	return sb.String()
}

// buildAbortedActionsSection 构建已中止部分：任务收口或目标调整时没做完就被收回的动作。
// 中止不含对错判断——与当前目标相关的可以重新提出。尾部逐条 + 更早折叠。
func buildAbortedActionsSection(actions []explorationgraph.Node) string {
	if len(actions) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## 已中止的动作（未执行完即被收回，不含对错判断）\n")
	shown, total := foldTerminalActions(actions)
	sb.WriteString(foldLine("已中止动作", total))
	for _, action := range shown {
		fmt.Fprintf(&sb, "- %s [ID: %s] %s（原因：%s）\n", actionTag("已中止", action), action.ID, actionInstruction(action), stateReason(action, "未注明"))
	}
	sb.WriteString("\n")
	return sb.String()
}

// rejecterLabel 是否决方的中文称谓（用户的否决是权威决定，规划者需要分辨）。
func rejecterLabel(by explorationgraph.SourceType) string {
	switch by {
	case explorationgraph.SourceUser:
		return "用户"
	case explorationgraph.SourceMonitor:
		return "监察者"
	default:
		return "未知"
	}
}

// stateReason 返回动作的状态原因（缺失时用 fallback）。
func stateReason(action explorationgraph.Node, fallback string) string {
	if action.StateReason != nil && *action.StateReason != "" {
		return *action.StateReason
	}
	return fallback
}

// buildResultsSection 构建已确认发现部分：按陈述去重（同义重复的判定
// 不重复占视野），逐条钳长。
func buildResultsSection(results []explorationgraph.Node) string {
	if len(results) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## 已坐实的结果\n")
	seen := make(map[string]bool, len(results))
	for _, result := range results {
		var r struct {
			Statement string `json:"statement"`
		}
		if err := json.Unmarshal(result.Content, &r); err != nil {
			fmt.Fprintf(&sb, "- (解析失败: %v)\n", err)
			continue
		}
		if seen[r.Statement] {
			continue
		}
		seen[r.Statement] = true
		// ID 必须展示：新动作的 triggered_by 因果声明引用它（发现滋生动作）。
		fmt.Fprintf(&sb, "- [%s] %s\n", result.ID, textx.TruncateRunes(r.Statement, resultClampRunes, "…（截断）"))
	}
	sb.WriteString("\n")
	return sb.String()
}

// buildOpenLeadsSection 构建待深挖的线索：执行者已探通、尚未推进到结论的路径，单列在
// 事实之前，带 ID 供动作的 pursues_lead 认领。同一陈述只列一条——它说的是同一条路。
func buildOpenLeadsSection(leads []OpenLead) string {
	if len(leads) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("## 待深挖的线索（执行者已探通、尚未推进到结论）\n")
	seen := make(map[string]bool, len(leads))
	for _, lead := range leads {
		st := textx.TruncateRunes(lead.Statement, factClampRunes, "…（截断）")
		if seen[st] {
			continue
		}
		seen[st] = true
		fmt.Fprintf(&sb, "- [%s] %s\n", lead.ID, st)
	}
	sb.WriteString("\n")
	return sb.String()
}

// buildDeclinedLeadsSection 构建已判定不追的线索，带当初的理由：规划者据此不再推进它们。
// 它们只增不减，尾部逐条渲染、更早的折叠成计数行（与终态动作同一口径）。
func buildDeclinedLeadsSection(leads []DeclinedLead) string {
	if len(leads) == 0 {
		return ""
	}
	shown, folded := foldTail(leads, declinedLeadFoldLimit)
	var sb strings.Builder
	sb.WriteString("## 已判定不追的线索\n")
	sb.WriteString(foldLine("已判定不追的线索", folded))
	for _, lead := range shown {
		fmt.Fprintf(&sb, "- %s（理由：%s）\n",
			textx.TruncateRunes(lead.Statement, factClampRunes, "…（截断）"),
			textx.TruncateRunes(lead.Reason, reasonClampRunes, "…（截断）"))
	}
	sb.WriteString("\n")
	return sb.String()
}

// inventoryFactCategories 是对象清单类的事实类别：说明「有什么」，侦查期产出最多。
// 写指令时逐字要用的是另外三类（怎么进入 / 怎么连接 / 怎么反应）。
var inventoryFactCategories = []explorationgraph.ObservationCategory{
	explorationgraph.CategoryEntity,
	explorationgraph.CategoryNote,
}

// buildFactsSection 构建已确认事实部分——规划基底。事实由产出它的动作背书（写入即
// 终态），怎样利用由章程说明。
//
// 去重按陈述全文：陈述完全相同的事实说的是同一件事；只按截断后的文本去重，会把开头
// 相同、后半不同的两条事实错并成一条，被并掉的那条就此从规划者眼前消失。
//
// 逐条渲染的名额只分给对象清单类：它们说明「有什么」，侦查期产出最多，合计只留最新的
// inventoryRenderLimit 条，更早的折叠成计数行、细节经 graph_read 按需读。另外三类（怎么
// 进入 / 怎么连接 / 怎么反应）不占这个名额，全量渲染。
func buildFactsSection(facts []Fact) string {
	if len(facts) == 0 {
		return ""
	}

	// 事实按创建序传入，自最新向回收纳；同文只留最新的一条。归不了已知类别的记入「其他」。
	dedup := make([]Fact, 0, len(facts))
	seen := make(map[string]bool, len(facts))
	for i := len(facts) - 1; i >= 0; i-- {
		statement := facts[i].Statement
		if statement == "" || seen[statement] {
			continue
		}
		seen[statement] = true
		category := facts[i].Category
		if _, known := factCategoryHeadings[category]; !known {
			category = explorationgraph.CategoryNote
		}
		dedup = append(dedup, Fact{Category: category, Statement: statement})
	}

	// 对象清单类的名额在 dedup 上取：它是跨类别的新旧序，两个类别合起来留最新的。分桶之后
	// 再取就只剩桶内先后，一类会把另一类更近的挤掉。dedup 新的在前，转成时间序才能截尾部。
	inventory := make([]Fact, 0, len(dedup))
	for i := len(dedup) - 1; i >= 0; i-- {
		if slices.Contains(inventoryFactCategories, dedup[i].Category) {
			inventory = append(inventory, dedup[i])
		}
	}
	inventory, inventoryFolded := foldTail(inventory, inventoryRenderLimit)
	kept := make(map[string]bool, len(inventory))
	for _, f := range inventory {
		kept[f.Statement] = true
	}

	// 按类别分桶（桶内近者在前）；被折叠的对象清单类事实不再逐条渲染。
	byCategory := make(map[explorationgraph.ObservationCategory][]Fact, len(explorationgraph.FactCategories))
	for _, f := range dedup {
		if slices.Contains(inventoryFactCategories, f.Category) && !kept[f.Statement] {
			continue
		}
		byCategory[f.Category] = append(byCategory[f.Category], f)
	}

	var sb strings.Builder
	sb.WriteString("## 已确认的事实\n")
	if inventoryFolded > 0 {
		fmt.Fprintf(&sb, "- （另有 %d 条较早的对象类事实未逐条列出，需要时用 graph_read 深挖）\n", inventoryFolded)
	}
	for _, category := range explorationgraph.FactCategories {
		if len(byCategory[category]) == 0 {
			continue
		}
		fmt.Fprintf(&sb, "### %s\n", factCategoryHeadings[category])
		for _, f := range byCategory[category] {
			fmt.Fprintf(&sb, "- %s\n", textx.TruncateRunes(f.Statement, factClampRunes, "…（截断）"))
		}
	}
	sb.WriteString("\n")
	return sb.String()
}

// factCategoryHeadings 是事实各类别在规划提示里的标题（与 FactCategories 一一对应）。
var factCategoryHeadings = map[explorationgraph.ObservationCategory]string{
	explorationgraph.CategoryEntity:    "有什么（对象与属性）",
	explorationgraph.CategoryAccess:    "怎么进入（入口、认证与凭据）",
	explorationgraph.CategoryStructure: "怎么连接（关系与布局）",
	explorationgraph.CategoryBehavior:  "怎么反应（处理机制与限制）",
	explorationgraph.CategoryNote:      "其他",
}

// refuteReasonHint 陈述反驳原因的含义：证据没拿到（配方或上下文问题）与证据到了但主张不
// 成立是两回事，规划者据此决定换配方再试还是放弃方向（处置写在章程里）。原因字面量与
// evaluator 包的 refute_reason 常量一致（metadata JSON 契约）。
func refuteReasonHint(reason string) string {
	switch reason {
	case evaluator.RefuteNotReproducible:
		return "（not_reproducible：没取得主张成立的证据——复现不出、证据不足或受外部干扰，主张未被否定）"
	case evaluator.RefuteInvalidRecipe:
		return "（invalid_recipe：配方损坏——配方形状不对，主张未被检验）"
	case evaluator.RefuteAssertNotDiscriminative:
		return "（assert_not_discriminative：断言无鉴别力——它在正常情况下也会成立，配方构不成证据）"
	case evaluator.RefuteClaimInvalid:
		return "（claim_invalid：证据已取得，但不支持主张）"
	case evaluator.RefuteJudgeUnavailable:
		return "（judge_unavailable：评估环节未完成，主张未被检验）"
	default:
		return "（经复现门判定不成立）"
	}
}

// buildPendingHypothesesSection 构建待验证假设部分：已上报、正在复现门里验证的主张陈述
// （按陈述去重）。怎样对待它们由章程说明。
func buildPendingHypothesesSection(statements []string) string {
	if len(statements) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("## 待验证的假设（复现门验证中）\n")
	seen := make(map[string]bool, len(statements))
	for _, s := range statements {
		st := textx.TruncateRunes(s, historyClampRunes, "…（截断）")
		if seen[st] {
			continue
		}
		seen[st] = true
		fmt.Fprintf(&sb, "- %s\n", st)
	}
	sb.WriteString("\n")
	return sb.String()
}

// buildRefutedHypothesesSection 构建已证伪假设部分（含反驳原因分类与评估者的理由）
func buildRefutedHypothesesSection(hypotheses []RefutedHypothesis) string {
	if len(hypotheses) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## 已被证伪的假设（复现门判定）\n")
	seen := make(map[string]bool, len(hypotheses))
	for _, h := range hypotheses {
		st := textx.TruncateRunes(h.Statement, historyClampRunes, "…（截断）")
		if seen[st] {
			continue
		}
		seen[st] = true
		fmt.Fprintf(&sb, "- %s %s", st, refuteReasonHint(h.Reason))
		if detail := strings.TrimSpace(h.Detail); detail != "" {
			sb.WriteString("；评估说明：" + textx.TruncateRunes(detail, historyClampRunes, "…（截断）"))
		}
		sb.WriteString("\n")
	}
	sb.WriteString("\n")
	return sb.String()
}

// buildKnowledgeQueriesSection 列出本次运行已发出的知识库查询（每轮规划看不到前几轮的检索）。
func buildKnowledgeQueriesSection(queries []string) string {
	if len(queries) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("## 本次运行已检索过的知识库查询\n")
	for _, q := range queries {
		fmt.Fprintf(&sb, "- %s\n", textx.TruncateRunes(q, historyClampRunes, "…（截断）"))
	}
	sb.WriteString("\n")
	return sb.String()
}
