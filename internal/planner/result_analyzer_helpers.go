package planner

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/V3teran/liusha/internal/explorationgraph"
)

// buildAnalysisHeader 构建分析提示的头部
func buildAnalysisHeader() string {
	return "你是探索系统的分析专家。你的职责：分析探索结果，判断当前方向是否穷尽，决定是继续深挖还是切换方向。\n\n"
}

// buildActionCountWarning 根据 Action 数量构建警告信息
func buildActionCountWarning(actionCount int) string {
	var sb strings.Builder
	sb.WriteString("⚠️ **核心原则：保持探索路线多样性，避免单一方向过载**\n\n")
	fmt.Fprintf(&sb, "**当前探索状态**：当前 Objective 下已有 **%d 个 Actions**。\n", actionCount)

	switch {
	case actionCount > 50:
		sb.WriteString("⚠️ **警告：Actions 数量过多（>50）**，当前方向很可能已穷尽或陷入无效循环。**强烈建议生成 new_objectives（切换到新方向）**。\n\n")
	case actionCount > 20:
		sb.WriteString("⚠️ **注意：Actions 数量较多（>20）**，如果目标仍未达成，**倾向于生成 new_objectives（切换到新方向）**。\n\n")
	default:
		sb.WriteString("当前方向探索适中，可根据 Results 内容决定是继续深挖还是切换方向。\n\n")
	}

	return sb.String()
}

// buildCurrentObjectiveSection 构建当前 Objective 的描述
func buildCurrentObjectiveSection(currentObjective explorationgraph.Node) string {
	var sb strings.Builder
	sb.WriteString("## 当前 Objective\n\n")

	var objContent map[string]interface{}
	if err := json.Unmarshal(currentObjective.Content, &objContent); err != nil {
		fmt.Fprintf(&sb, "(目标解析失败: %v)\n\n", err)
	} else if desc, ok := objContent["description"].(string); ok {
		fmt.Fprintf(&sb, "目标：%s\n\n", desc)
	}

	return sb.String()
}

// buildAnalysisResultsSection 构建已有 Results 的描述
func buildAnalysisResultsSection(results []explorationgraph.Node) string {
	var sb strings.Builder
	sb.WriteString("## 已有的 Results\n\n")

	for i, result := range results {
		var content map[string]interface{}
		if err := json.Unmarshal(result.Content, &content); err != nil {
			fmt.Fprintf(&sb, "%d. Result ID: %s (解析失败: %v)\n", i+1, result.ID, err)
			continue
		}
		fmt.Fprintf(&sb, "%d. Result ID: %s\n", i+1, result.ID)
		if summary, ok := content["summary"].(string); ok {
			fmt.Fprintf(&sb, "   内容：%s\n", summary)
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

// buildDecisionGuide 构建决策流程指导
func buildDecisionGuide() string {
	var sb strings.Builder
	sb.WriteString("## 决策流程\n\n")

	// 1. 完成判断
	sb.WriteString("### 1. 当前 Objective 是否已完成？\n")
	sb.WriteString("- 目标已真正达成（发现目标漏洞 / 获得目标成果）\n")
	sb.WriteString("- Results 提供了充分证据\n")
	sb.WriteString("- 如果是 → 设置 `completed=true`，提供 `evidence_ids`\n\n")

	// 2. 穷尽判断
	sb.WriteString("### 2. 当前方向是否已穷尽？（关键判断）\n")
	sb.WriteString("判断标准：\n")
	sb.WriteString("- **已尝试所有明显探索点**：该入口/路径/节点的常见方法已全部尝试\n")
	sb.WriteString("- **出现重复失败模式**：多次尝试同类方法均失败，无新思路\n")
	sb.WriteString("- **遇到硬性阻塞**：权限限制、资源不可达等无法绕过的障碍\n")
	sb.WriteString("- **Results 暗示方向错误**：反馈表明该路径不可行\n\n")
	sb.WriteString("**如果当前方向已穷尽** → 优先生成 `new_objectives`（切换到本质不同的新方向）\n\n")

	// 3. 决策分支
	sb.WriteString("### 3. 新方向 vs 当前方向延续（决策分支）\n\n")

	// new_objectives 条件
	sb.WriteString("#### 生成 new_objectives（新方向）的条件：\n")
	sb.WriteString("- 当前方向已穷尽（见第 2 步）\n")
	sb.WriteString("- **或** 当前 Objective 下的 Actions 已经很多（>20 个），但目标未达成\n")
	sb.WriteString("- Results 暗示存在**本质不同**的探索面：\n")
	sb.WriteString("  - 不同入口点（如发现新路径、新接口、新功能模块）\n")
	sb.WriteString("  - 不同探索链（如从一种方法切换到另一种完全不同的方法）\n")
	sb.WriteString("  - 不同资源类型（如从一类资源切换到另一类资源）\n\n")
	sb.WriteString("new_objectives 示例：\n")
	sb.WriteString("- ❌ 错误：\"继续探索当前路径\"（这是延续，不是新方向）\n")
	sb.WriteString("- ✅ 正确：\"探索备用路径的可访问性\"（不同入口点）\n")
	sb.WriteString("- ✅ 正确：\"测试其他功能模块的可用性\"（不同探索链）\n\n")

	// new_actions 条件
	sb.WriteString("#### 生成 new_actions（当前方向延续）的条件：\n")
	sb.WriteString("- 当前方向**未穷尽**\n")
	sb.WriteString("- Results 揭示了**当前范围内**的新探索点：\n")
	sb.WriteString("  - 新参数/字段需要探索\n")
	sb.WriteString("  - 需要尝试新方法变种\n")
	sb.WriteString("  - 发现可利用的细节\n\n")
	sb.WriteString("new_actions 示例：\n")
	sb.WriteString("- ✅ 正确：\"探索发现的新参数的可能值\"\n")
	sb.WriteString("- ✅ 正确：\"在特定字段尝试其他输入方法\"\n\n")

	// 空响应
	sb.WriteString("### 4. 什么都不生成也是正常的\n")
	sb.WriteString("- 当前方向已穷尽，但暂时找不到新方向 → 空数组\n")
	sb.WriteString("- Results 不包含可操作信息 → 空数组\n\n")

	return sb.String()
}

// buildResponseFormat 构建响应格式说明
func buildResponseFormat() string {
	var sb strings.Builder
	sb.WriteString("## 响应格式\n\n")
	sb.WriteString("```json\n")
	sb.WriteString("{\n")
	sb.WriteString("  \"completed\": false,\n")
	sb.WriteString("  \"evidence_ids\": [],\n")
	sb.WriteString("  \"reasoning\": \"详细说明你的判断逻辑：当前方向是否穷尽？为什么选择新方向/延续？\",\n")
	sb.WriteString("  \"new_objectives\": [\n")
	sb.WriteString("    {\n")
	sb.WriteString("      \"description\": \"本质不同的新方向（不同入口/攻击链/资产）\",\n")
	sb.WriteString("      \"priority\": \"high\",\n")
	sb.WriteString("      \"triggered_by\": [\"result_id_1\"],\n")
	sb.WriteString("      \"reasoning\": \"为什么需要切换到这个新方向\"\n")
	sb.WriteString("    }\n")
	sb.WriteString("  ],\n")
	sb.WriteString("  \"new_actions\": [\n")
	sb.WriteString("    {\n")
	sb.WriteString("      \"instruction\": \"当前范围内的新测试点\",\n")
	sb.WriteString("      \"priority\": \"medium\",\n")
	sb.WriteString("      \"triggered_by\": [\"result_id_1\"],\n")
	sb.WriteString("      \"reasoning\": \"为什么这个测试点还值得尝试\"\n")
	sb.WriteString("    }\n")
	sb.WriteString("  ]\n")
	sb.WriteString("}\n")
	sb.WriteString("```\n\n")
	return sb.String()
}

// buildKeyReminders 构建关键提示
func buildKeyReminders() string {
	var sb strings.Builder
	sb.WriteString("**关键提示**：\n")
	sb.WriteString("- 当前方向 Actions 很多但未成功时，**大概率应该切换方向（生成 new_objectives）**\n")
	sb.WriteString("- new_objectives 和 new_actions 的区别：前者是「本质不同的方向」，后者是「当前方向的深挖」\n")
	sb.WriteString("- 两者可以同时存在：当前方向还能挖一点，但同时发现了新方向\n")
	sb.WriteString("- 直接返回纯 JSON，不要用 Markdown 代码块标记\n")
	return sb.String()
}
