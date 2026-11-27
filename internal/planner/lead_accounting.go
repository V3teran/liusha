package planner

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/framework/runtime"
	plannercontract "github.com/V3teran/liusha/internal/planner/contract"
)

// 短号：规划输入给在办的动作、待深挖的线索编的短号（A1、L1）。模型按短号引用，不必
// 照抄 UUID；短号就是它在输入清单里的序号，规划输出按同一份清单解析。
const (
	actionPrefix = "A"
	leadPrefix   = "L"
)

func ordinal(prefix string, index int) string { return fmt.Sprintf("%s%d", prefix, index+1) }

// ordinalIndex 把短号解析成清单下标。前缀不分大小写：模型写 l2 与 L2 指同一条线索。
func ordinalIndex(prefix, handle string) (int, bool) {
	handle = strings.TrimSpace(handle)
	if len(handle) <= len(prefix) || !strings.EqualFold(handle[:len(prefix)], prefix) {
		return 0, false
	}
	n, err := strconv.Atoi(handle[len(prefix):])
	if err != nil || n < 1 {
		return 0, false
	}
	return n - 1, true
}

// canonical 是模型写的短号的规范写法（前缀大写）；不是这个前缀的短号时去空白后原样交回。
func canonical(prefix, handle string) string {
	if _, ok := ordinalIndex(prefix, handle); ok {
		return strings.ToUpper(strings.TrimSpace(handle))
	}
	return strings.TrimSpace(handle)
}

// actionID 把在办动作的短号解析成动作 ID。
func (c *PlanningContext) actionID(handle string) (string, bool) {
	i, ok := ordinalIndex(actionPrefix, handle)
	if !ok || i >= len(c.PendingActions) {
		return "", false
	}
	return c.PendingActions[i].ID, true
}

// actionHandle 是在办动作在输入里的短号；动作已不在办时为空。
func (c *PlanningContext) actionHandle(id string) string {
	if i := slices.IndexFunc(c.PendingActions, func(a explorationgraph.Node) bool { return a.ID == id }); i >= 0 {
		return ordinal(actionPrefix, i)
	}
	return ""
}

// leadIDs 把线索的短号解析成它指的线索：同一陈述的线索在输入里并成一行列出，引用其中
// 任意一个短号都指这一行上的全部线索——它们说的是同一条路。
func (c *PlanningContext) leadIDs(handle string) ([]string, bool) {
	i, ok := ordinalIndex(leadPrefix, handle)
	if !ok || i >= len(c.OpenLeads) {
		return nil, false
	}
	statement := c.OpenLeads[i].Statement
	ids := make([]string, 0, 1)
	for _, lead := range c.OpenLeads {
		if lead.Statement == statement {
			ids = append(ids, lead.ID)
		}
	}
	return ids, true
}

// leadProblem 是一条待深挖的线索在本轮规划里没有得到有效交代的说明：给模型补全，或落图时
// 记日志。
type leadProblem struct {
	handle  string // 模型用的线索短号（引用了不存在的短号时是它写的那个）
	message string
}

// leadPlan 是规划输出里对线索的交代，解析到图上引用之后的结果。
type leadPlan struct {
	claims   map[int][]string           // 动作在响应里的下标 → 它认领的线索 ID
	covers   []explorationgraph.Pursuit // 声明由图上在办的动作覆盖的线索
	closings []explorationgraph.LeadClosing
	left     int // 明确留待下轮的线索条数
	problems []leadProblem
}

// describe 把交代上的问题列成给模型或日志看的一句话一条。
func (p leadPlan) describe() []string {
	out := make([]string, 0, len(p.problems))
	for _, problem := range p.problems {
		out = append(out, problem.handle+"："+problem.message)
	}
	return out
}

// accountLeads 核对规划输出对每条待深挖线索的交代：每条恰一次——被本轮某个动作的 pursues
// 引用，或在 leads 里有一条成立的处置（cover / close / leave）。不成立的地方原样列出，
// 成立的部分照常保留：一条交代不成立不让其余的跟着作废。
func accountLeads(planCtx *PlanningContext, resp *PlanningResponse) leadPlan {
	plan := leadPlan{claims: map[int][]string{}}
	// covered 是已有成立交代的线索；touched 是输出里提到过的线索（交代成立与否不论）。
	covered := make(map[string]bool, len(planCtx.OpenLeads))
	touched := make(map[string]bool, len(planCtx.OpenLeads))
	// 输出里提到了这条线索：交代不成立的已就那条交代报过，末尾不再报一次「还没有交代」。
	touch := func(ids []string) {
		for _, id := range ids {
			touched[id] = true
		}
	}
	// 登记一条成立的交代：它指的线索里已有一条被交代过就不登记，返回是否成立。
	admit := func(ids []string) bool {
		if slices.ContainsFunc(ids, func(id string) bool { return covered[id] }) {
			return false
		}
		for _, id := range ids {
			covered[id] = true
		}
		return true
	}
	const (
		duplicated  = "被重复交代：一条线索本轮只由一个动作跟进、只交代一次——多出的认领去掉 pursues"
		unknownLead = "不是「待深挖的线索」里的编号"
	)

	for i, action := range resp.Actions {
		for _, handle := range action.Pursues {
			handle = canonical(leadPrefix, handle)
			ids, ok := planCtx.leadIDs(handle)
			if !ok {
				plan.problems = append(plan.problems, leadProblem{handle, unknownLead})
				continue
			}
			touch(ids)
			if !admit(ids) {
				plan.problems = append(plan.problems, leadProblem{handle, duplicated})
				continue
			}
			plan.claims[i] = append(plan.claims[i], ids...)
		}
	}

	for _, d := range resp.Leads {
		handle := canonical(leadPrefix, d.ID)
		ids, ok := planCtx.leadIDs(handle)
		if !ok {
			plan.problems = append(plan.problems, leadProblem{handle, unknownLead})
			continue
		}
		touch(ids)
		// 先校验、后登记：交代不成立的线索不算已交代，模型给的下一个版本仍可成立。
		switch strings.ToLower(strings.TrimSpace(d.Decision)) {
		case "cover":
			actionID, ok := planCtx.actionID(d.Action)
			if !ok {
				plan.problems = append(plan.problems, leadProblem{handle, "cover 的 action 不是「进行中的工作」里的在办动作编号"})
				continue
			}
			if !admit(ids) {
				plan.problems = append(plan.problems, leadProblem{handle, duplicated})
				continue
			}
			for _, id := range ids {
				plan.covers = append(plan.covers, explorationgraph.Pursuit{ActionID: actionID, LeadID: id})
			}
		case "close":
			kind := explorationgraph.LeadClosureKind(strings.ToLower(strings.TrimSpace(d.Kind)))
			if !kind.Declared() {
				plan.problems = append(plan.problems, leadProblem{handle, "close 的 kind 不在契约列出的类别里"})
				continue
			}
			if strings.TrimSpace(d.Reason) == "" {
				plan.problems = append(plan.problems, leadProblem{handle, "close 缺少 reason"})
				continue
			}
			if !admit(ids) {
				plan.problems = append(plan.problems, leadProblem{handle, duplicated})
				continue
			}
			for _, id := range ids {
				plan.closings = append(plan.closings, explorationgraph.LeadClosing{LeadID: id, Kind: kind, Reason: strings.TrimSpace(d.Reason)})
			}
		case "leave":
			if !admit(ids) {
				plan.problems = append(plan.problems, leadProblem{handle, duplicated})
				continue
			}
			plan.left += len(ids)
		case "":
			plan.problems = append(plan.problems, leadProblem{handle, "还没有交代"})
		default:
			plan.problems = append(plan.problems, leadProblem{handle, "decision 只能是 cover、close 或 leave"})
		}
	}

	// 输出里一条都没提到的线索按输入里的行报：同一行上的线索一起列出来。
	reported := make(map[string]bool, len(planCtx.OpenLeads))
	for _, lead := range planCtx.OpenLeads {
		if touched[lead.ID] || reported[lead.ID] {
			continue
		}
		handles := make([]string, 0, 1)
		for j, sibling := range planCtx.OpenLeads {
			if sibling.Statement == lead.Statement {
				handles = append(handles, ordinal(leadPrefix, j))
				reported[sibling.ID] = true
			}
		}
		plan.problems = append(plan.problems, leadProblem{strings.Join(handles, "、"), "还没有交代"})
	}
	return plan
}

// completeLeadAccounting 就没有成立的线索交代，接着刚才的对话追问模型一次：它看得到自己
// 上一版的输出与全部图面，补全的代价只是再生成一次。只追问一次——追问失败、补全后仍
// 不成立，都按最后一份能解析的输出落图：成立的部分照常生效，缺的线索本轮留待，这一轮
// 规划不因此作废。本轮被取消时原样交还错误。
func (e *Engine) completeLeadAccounting(
	ctx context.Context,
	provider llm.Provider,
	systemPrompt string,
	first *runtime.ReActResult,
	response *PlanningResponse,
	plan leadPlan,
	planCtx *PlanningContext,
	added []OpenLead,
) (*PlanningResponse, *runtime.ReActResult, leadPlan, error) {
	e.logger.Warn().Strs("problems", plan.describe()).Int("added", len(added)).Msg("待深挖的线索没有逐条交代，追问一次")

	var onThoughtDelta, onThinkingDelta func(string)
	if e.thoughts != nil {
		onThoughtDelta, onThinkingDelta = e.thoughts.Delta, e.thoughts.ThinkingDelta
	}
	// 追问不带工具：它要的都在刚才的对话里。ReActConfig.SystemPrompt 会排到初始历史之后，
	// 所以系统提示直接放进历史、排在最前（见 followUpHistory）。
	followUp := runtime.NewReActRuntime()
	next, err := followUp.Run(ctx, &runtime.ReActConfig{
		Objective:            leadFollowUp(planCtx, plan.problems, added),
		LLMProvider:          provider,
		MaxIterations:        e.maxIt,
		InitialHistory:       followUpHistory(first.MessageHistory, systemPrompt),
		OnThoughtDelta:       onThoughtDelta,
		OnThinkingDelta:      onThinkingDelta,
		MessageModifierChain: runtime.NewCompactionChain(e.contextBudget(ctx), "planner", e.distiller, e.compactionTimeouts),
		ToolOutputTailBytes:  e.toolOutputTailBytes,
		OutputFormatPrompt:   "你是规划者（Planner），需要输出下一批要执行的Action。",
		OutputFormatSchema:   plannercontract.Text,
		OutputCheck:          planningOutputCheck,
	})
	if err != nil {
		if ctx.Err() != nil {
			return nil, nil, plan, err
		}
		if next != nil {
			absorb(first, next)
		}
		e.logger.Warn().Err(err).Msg("追问线索交代失败，按上一版规划落图")
		return response, first, plan, nil
	}
	absorb(first, next)
	nudged, perr := e.parsePlanningResponse(next.FinalAnswer)
	if perr != nil {
		e.logger.Warn().Err(perr).Msg("追问后的规划输出不可解析，按上一版规划落图")
		return response, first, plan, nil
	}
	first.FinalAnswer, first.FinalThinking = next.FinalAnswer, next.FinalThinking
	if !nudged.ShouldContinue {
		return nudged, first, leadPlan{}, nil
	}
	return nudged, first, accountLeads(planCtx, nudged), nil
}

// leadFollowUp 是追问的输入：规划期间新出现的线索带着短号与处境列出，交代上的问题逐条列出，
// 并指回输出契约——格式只在契约里。
func leadFollowUp(planCtx *PlanningContext, problems []leadProblem, added []OpenLead) string {
	var b strings.Builder
	b.WriteString("上一版规划没有把「待深挖的线索」逐条交代完。")
	if len(added) > 0 {
		b.WriteString("规划进行期间图上又出现了待深挖的线索，编号接在输入清单之后：\n")
		for i, lead := range planCtx.OpenLeads {
			if slices.ContainsFunc(added, func(a OpenLead) bool { return a.ID == lead.ID }) {
				fmt.Fprintf(&b, "- [%s] %s（%s）\n", ordinal(leadPrefix, i), lead.Statement, planCtx.leadStatus(lead, planCtx.now()))
			}
		}
	}
	b.WriteString("按「输出契约」重新输出完整的规划 JSON（不是只补差额）：\n")
	for _, problem := range problems {
		fmt.Fprintf(&b, "- %s：%s\n", problem.handle, problem.message)
	}
	return b.String()
}

// followUpHistory 是追问的初始消息历史：系统提示仍在最前（沿用第一轮实际发出去的那一份，
// 压缩过的也是它），随后是第一轮的全部往来。
func followUpHistory(history []llm.Message, systemPrompt string) []llm.Message {
	out := make([]llm.Message, 0, len(history)+1)
	for _, m := range history {
		if m.Role == llm.RoleSystem {
			out = append(out, m)
		}
	}
	if len(out) == 0 {
		out = append(out, llm.Message{Role: llm.RoleSystem, Content: systemPrompt})
	}
	for _, m := range history {
		if m.Role != llm.RoleSystem {
			out = append(out, m)
		}
	}
	return out
}

// absorb 把追问这次运行的用量与耗时并进首轮：时间线上这一轮的用量与「等了多久」是两次
// 生成之和。
func absorb(round, extra *runtime.ReActResult) {
	round.Usage = round.Usage.Add(extra.Usage)
	round.ElapsedMs += extra.ElapsedMs
	round.Iterations += extra.Iterations
}

// closureLabel 是线索关闭类别在规划输入与时间线里的说法。
func closureLabel(kind explorationgraph.LeadClosureKind) string {
	switch kind {
	case explorationgraph.ClosureNotWorth:
		return "价值够不上一个动作"
	case explorationgraph.ClosureOutOfScope:
		return "超出任务目标"
	case explorationgraph.ClosurePremiseRefuted:
		return "前提已证伪"
	case explorationgraph.ClosureExhausted:
		return "已走到头"
	case explorationgraph.ClosureBlocked:
		return "受阻"
	case explorationgraph.ClosureConcluded:
		return "已有结论"
	case explorationgraph.ClosureDry:
		return "干涸"
	case explorationgraph.ClosureVetoed:
		return "被否决关闭"
	}
	return string(kind)
}
