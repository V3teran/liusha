package planner

import (
	"github.com/V3teran/liusha/internal/evaluator"

	"encoding/json"
	"fmt"
	"strings"

	"github.com/V3teran/liusha/internal/explorationgraph"
)

// buildPlanningPromptHeader 构建 prompt 头部
func buildPlanningPromptHeader() string {
	var sb strings.Builder
	sb.WriteString("你是一个探索规划专家。根据当前任务状态，决定下一步应该执行的操作。\n\n")

	sb.WriteString("## 核心原则：智能探索模式\n")
	sb.WriteString("- 你需要**主动判断**当前探索方向是否已充分\n")
	sb.WriteString("- 根据探索内容的实质判断，而非机械计数\n")
	sb.WriteString("- 不要在同一方向无限探索，也不要过早放弃\n\n")

	return sb.String()
}

// buildStopCriteria 构建停止条件说明
func buildStopCriteria() string {
	var sb strings.Builder
	sb.WriteString("## 何时停止当前方向（should_continue=false）\n\n")
	sb.WriteString("**判断标准**（根据实际情况灵活判断）：\n\n")
	sb.WriteString("1. **内容维度**（最重要）\n")
	sb.WriteString("   - 当前方向的主要探索点已基本覆盖\n")
	sb.WriteString("   - 近期 Actions 出现重复或相似模式\n")
	sb.WriteString("   - 新 Actions 的边际收益明显递减\n")
	sb.WriteString("   - 已有发现足以支撑新的探索方向\n\n")

	sb.WriteString("2. **深度参考**（仅供参考，不是硬性要求）\n")
	sb.WriteString("   - 简单方向：5-10 个动作\n")
	sb.WriteString("   - 常规方向：10-20 个动作\n")
	sb.WriteString("   - 复杂方向：20-30+ 动作\n\n")

	sb.WriteString("**重要**：主动判断是否应该停止，不要等到无事可做！\n")
	sb.WriteString("停止后，系统会自动从 Results 中提取新的探索方向。\n\n")

	return sb.String()
}

// buildObjectiveSection 构建任务目标部分：根目标为章程，全部目标（含派生方向）逐条列出。
func buildObjectiveSection(objective string, objectives []explorationgraph.Node) string {
	var sb strings.Builder
	sb.WriteString("## 任务目标（章程）\n")
	if objective != "" {
		sb.WriteString(objective)
	} else {
		sb.WriteString("（未指定明确目标）")
	}
	sb.WriteString("\n\n")

	if len(objectives) > 1 {
		sb.WriteString("### 全部目标（含派生方向）\n")
		for i, obj := range objectives {
			desc := ""
			var c struct {
				Description string `json:"description"`
			}
			if json.Unmarshal(obj.Content, &c) == nil {
				desc = c.Description
			}
			if desc == "" {
				continue
			}
			if i == 0 {
				fmt.Fprintf(&sb, "- [根目标] %s\n", desc)
			} else {
				fmt.Fprintf(&sb, "- [派生目标] %s\n", desc)
			}
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// buildStatisticsSection 构建统计信息部分
func buildStatisticsSection(ctx *PlanningContext) string {
	var sb strings.Builder
	sb.WriteString("## 当前探索统计\n")
	fmt.Fprintf(&sb, "- 已完成 Actions: %d\n", len(ctx.CompletedActions))
	fmt.Fprintf(&sb, "- 进行中 Actions: %d\n", len(ctx.PendingActions))
	fmt.Fprintf(&sb, "- 已确认 Results: %d\n", len(ctx.Results))
	if len(ctx.CompletedActions) >= 20 {
		sb.WriteString("- 提示：当前方向已探索较深，建议评估是否应该切换方向\n")
	}
	sb.WriteString("\n")
	return sb.String()
}

// buildCompletedActionsSection 构建已完成工作部分
func buildCompletedActionsSection(actions []explorationgraph.Node) string {
	var sb strings.Builder
	sb.WriteString("## 已完成的工作\n")
	if len(actions) > 0 {
		for _, action := range actions {
			var a struct {
				Instruction string `json:"instruction"`
			}
			if err := json.Unmarshal(action.Content, &a); err != nil {
				fmt.Fprintf(&sb, "- [完成] (解析失败: %v)\n", err)
				continue
			}
			fmt.Fprintf(&sb, "- [完成] [ID: %s] %s\n", action.ID, a.Instruction)
		}
	} else {
		sb.WriteString("（尚未完成任何操作）\n")
	}
	sb.WriteString("\n")
	return sb.String()
}

// buildPendingActionsSection 构建进行中工作部分
func buildPendingActionsSection(actions []explorationgraph.Node) string {
	if len(actions) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## 进行中的工作\n")
	for _, action := range actions {
		var a struct {
			Instruction string `json:"instruction"`
		}
		if err := json.Unmarshal(action.Content, &a); err != nil {
			fmt.Fprintf(&sb, "- [进行中] (解析失败: %v)\n", err)
			continue
		}
		fmt.Fprintf(&sb, "- [进行中] [ID: %s] %s\n", action.ID, a.Instruction)
	}
	sb.WriteString("\n")
	return sb.String()
}

// buildFailedActionsSection 构建失败尝试部分
func buildFailedActionsSection(actions []explorationgraph.Node) string {
	if len(actions) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## 失败的尝试\n")
	for _, action := range actions {
		var a struct {
			Instruction string `json:"instruction"`
		}
		if err := json.Unmarshal(action.Content, &a); err != nil {
			fmt.Fprintf(&sb, "- [失败] (解析失败: %v)\n", err)
			continue
		}

		reason := "未知原因"
		if action.State != nil && *action.State == explorationgraph.StateFailed {
			var fullAction struct {
				Instruction string `json:"instruction"`
				Reason      string `json:"reason"`
			}
			if err := json.Unmarshal(action.Content, &fullAction); err == nil && fullAction.Reason != "" {
				reason = fullAction.Reason
			}
		}
		fmt.Fprintf(&sb, "- [失败] [ID: %s] %s（原因：%s）\n", action.ID, a.Instruction, reason)
	}
	sb.WriteString("\n")
	return sb.String()
}

// buildAbortedActionsSection 构建外部纠偏中止部分——被 monitor kill 的动作
// 连同 blocked_reason 呈现，规划者据此不再派等效方向（否则 kill 反馈回路断一半）。
func buildAbortedActionsSection(actions []explorationgraph.Node) string {
	if len(actions) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## 被外部纠偏中止的动作（禁止等效重提）\n")
	for _, action := range actions {
		var a struct {
			Instruction string `json:"instruction"`
		}
		if err := json.Unmarshal(action.Content, &a); err != nil {
			fmt.Fprintf(&sb, "- [已中止] (解析失败: %v)\n", err)
			continue
		}
		reason := "未注明"
		if action.Metadata != nil {
			var m struct {
				BlockedReason string `json:"blocked_reason"`
			}
			if json.Unmarshal(action.Metadata, &m) == nil && m.BlockedReason != "" {
				reason = m.BlockedReason
			}
		}
		fmt.Fprintf(&sb, "- [已中止] [ID: %s] %s（纠偏原因：%s）\n", action.ID, a.Instruction, reason)
	}
	sb.WriteString("\n")
	return sb.String()
}

// buildBlackboardSection 构建经验黑板部分——跨 agent 确认经验的摘要行
// （cognition 注入，格式化完成），规划者直接消费执行者的经验。
func buildBlackboardSection(lines []string) string {
	if len(lines) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## 经验黑板（跨 agent 已确认经验）\n")
	for _, line := range lines {
		fmt.Fprintf(&sb, "- %s\n", line)
	}
	sb.WriteString("\n")
	return sb.String()
}

// buildResultsSection 构建已确认发现部分
func buildResultsSection(results []explorationgraph.Node) string {
	if len(results) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## 已确认的发现\n")
	for _, result := range results {
		var r struct {
			Summary string `json:"summary"`
		}
		if err := json.Unmarshal(result.Content, &r); err != nil {
			fmt.Fprintf(&sb, "- (解析失败: %v)\n", err)
			continue
		}
		fmt.Fprintf(&sb, "- %s\n", r.Summary)
	}
	sb.WriteString("\n")
	return sb.String()
}

// buildFactsSection 构建已确认世界状态部分——规划基底：已建立的会话、
// 已探明的机制、已确认的资源。事实由执行轨迹背书（写入即终态），直接
// 消费即可，禁止把已确认的事实重做成探索动作。
func buildFactsSection(facts []Fact) string {
	if len(facts) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## 已确认的世界状态（直接可用，无需重新探索）\n")
	for _, f := range facts {
		fmt.Fprintf(&sb, "- %s\n", f.Statement)
	}
	sb.WriteString("\n**注意**：以上事实已确认成立。规划时直接利用（如复用已建立的会话、沿已探明的机制推导），不要生成重复确认它们的动作。\n\n")
	return sb.String()
}

// refuteReasonHint 把反驳原因翻译成规划指令：配方类失败（证据没拿到）可换姿势
// 重试，语义证伪（证据到了但不构成漏洞）才是方向作废——两者处置不同。
// 原因字面量与 evaluator 包的 refute_reason 常量保持一致（metadata JSON 契约）。
func refuteReasonHint(reason string) string {
	switch reason {
	case evaluator.RefuteNotReproducible:
		return "（复现失败：配方或上下文问题——可换配方/换方法重试，不要原样重提）"
	case evaluator.RefuteInvalidRecipe:
		return "（配方损坏：可换有效配方重试）"
	case evaluator.RefuteAssertNotDiscriminative:
		return "（断言无鉴别力被质量门拒绝：换捕捉利用响应独有特征的断言重提）"
	case evaluator.RefuteClaimInvalid:
		return "（复现成立但主张不成立：此方向作废，除非有实质新证据）"
	default:
		return "（经真实复现验证被否定：不要原样重提）"
	}
}

// buildRefutedHypothesesSection 构建已证伪假设部分（含反驳原因分类）
func buildRefutedHypothesesSection(hypotheses []RefutedHypothesis) string {
	if len(hypotheses) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## 已被证伪的假设（复现门裁决）\n")
	for _, h := range hypotheses {
		fmt.Fprintf(&sb, "- %s %s\n", h.Statement, refuteReasonHint(h.Reason))
	}
	sb.WriteString("\n**注意**：语义证伪（claim_invalid）的假设禁止同方向重提；复现失败类（not_reproducible/invalid_recipe/assert_not_discriminative）允许换配方换断言再试，但都不要原样重提。\n\n")
	return sb.String()
}

// buildPlanningRequirements 构建规划要求部分
func buildPlanningRequirements() string {
	var sb strings.Builder
	sb.WriteString("## 规划要求\n\n")
	sb.WriteString("请基于以上信息，提出下一步的探索操作。\n\n")

	sb.WriteString("**继续探索的判断标准**：\n")
	sb.WriteString("- 从已有发现中寻找新的探索线索\n")
	sb.WriteString("- 对成功的操作进行深入探索\n")
	sb.WriteString("- 对失败的操作尝试替代方案\n")
	sb.WriteString("- 横向扩展到相关领域\n")
	sb.WriteString("- 只有在确实无法继续时才设置 should_continue=false\n\n")

	sb.WriteString("**Action 类型参考**（自由描述，用于执行指导分派）：\n")
	sb.WriteString("- reconnaissance: 信息收集\n")
	sb.WriteString("- analysis: 分析研究\n")
	sb.WriteString("- testing: 实测验证\n\n")

	sb.WriteString("**复杂度级别**：\n")
	sb.WriteString("- simple: 简单操作（快速执行）\n")
	sb.WriteString("- moderate: 中等复杂度（常规操作）\n")
	sb.WriteString("- complex: 复杂操作（深度分析）\n\n")

	sb.WriteString("**优先级**：\n")
	sb.WriteString("- critical: 阻塞后续工作\n")
	sb.WriteString("- high: 重要且紧急\n")
	sb.WriteString("- medium: 常规优先级\n")
	sb.WriteString("- low: 可选补充\n\n")

	return sb.String()
}

// buildJSONSchema 构建 JSON 格式示例
func buildJSONSchema() string {
	var sb strings.Builder
	sb.WriteString("请以 JSON 格式返回你的规划：\n")
	sb.WriteString("```json\n")
	sb.WriteString("{\n")
	sb.WriteString("  \"should_continue\": true/false,\n")
	sb.WriteString("  \"reasoning\": \"你的推理过程\",\n")
	sb.WriteString("  \"actions\": [\n")
	sb.WriteString("    {\n")
	sb.WriteString("      \"type\": \"action类型\",\n")
	sb.WriteString("      \"instruction\": \"具体执行指令\",\n")
	sb.WriteString("      \"complexity\": \"simple/moderate/complex\",\n")
	sb.WriteString("      \"priority\": \"critical/high/medium/low\",\n")
	sb.WriteString("      \"reason\": \"为什么需要这个操作\",\n")
	sb.WriteString("      \"depends_on\": [\"可引用上面列出的真实 Action ID；列表里没有可依赖的就填 []，绝不要编造或照抄示例\"],\n")
	sb.WriteString("      \"metadata\": {}\n")
	sb.WriteString("    }\n")
	sb.WriteString("  ]\n")
	sb.WriteString("}\n")
	sb.WriteString("```\n")
	return sb.String()
}
