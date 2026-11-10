package planner

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/V3teran/liusha/internal/explorationgraph"
)

// objectiveView 是结果分析视角下的目标划分。
type objectiveView struct {
	task    string                  // 任务目标描述（根目标）
	live    []explorationgraph.Node // 进行中的派生目标（open）：可宣告达成、可被吸收
	settled []explorationgraph.Node // 已收敛的派生目标：只供去重，不可引用
}

// newObjectiveView 按「根目标 / 进行中的派生目标 / 已收敛的派生目标」划分
// （objectives 按创建序升序；多 host 任务的各根目标共用同一 brief）。
func newObjectiveView(objectives []explorationgraph.Node) objectiveView {
	var view objectiveView
	if root, ok := explorationgraph.RootObjective(objectives); ok {
		view.task = objectiveDescription(root)
	}
	for i := range objectives {
		obj := objectives[i]
		if !obj.IsDerivedObjective() {
			continue
		}
		if obj.State != nil && *obj.State == explorationgraph.StateOpen {
			view.live = append(view.live, obj)
		} else {
			view.settled = append(view.settled, obj)
		}
	}
	return view
}

// objectiveDescription 读取 objective.content.description（缺失时回落为 ID）。
func objectiveDescription(obj explorationgraph.Node) string {
	var c struct {
		Description string `json:"description"`
	}
	if json.Unmarshal(obj.Content, &c) == nil && c.Description != "" {
		return c.Description
	}
	return obj.ID
}

// settledLabel 是已收敛派生目标的展示标签。
func settledLabel(obj explorationgraph.Node) string {
	if obj.State != nil && *obj.State == explorationgraph.StateSuperseded {
		return "已吸收"
	}
	if obj.State != nil && *obj.State == explorationgraph.StateCompleted {
		return "已达成"
	}
	return "已结束"
}

// buildAnalysisHeader 构建分析提示的头部
func buildAnalysisHeader() string {
	return "你是探索系统的分析专家。你的职责：分析探索结果，判断当前方向是否穷尽，决定是继续深挖还是切换方向，并维护派生目标清单。\n\n"
}

// buildActionCountWarning 根据 Action 数量构建警告信息
func buildActionCountWarning(actionCount int) string {
	var sb strings.Builder
	sb.WriteString("**核心原则：保持探索路线多样性，避免单一方向过载**\n\n")
	fmt.Fprintf(&sb, "**当前探索状态**：任务已有 **%d 个 Actions**。\n", actionCount)

	switch {
	case actionCount > 50:
		sb.WriteString("**警告：Actions 数量过多（>50）**，当前方向很可能已穷尽或陷入无效循环。**强烈建议生成 new_objectives（切换到新方向）**。\n\n")
	case actionCount > 20:
		sb.WriteString("**注意：Actions 数量较多（>20）**，如果目标仍未达成，**倾向于生成 new_objectives（切换到新方向）**。\n\n")
	default:
		sb.WriteString("当前方向探索适中，可根据 Results 内容决定是继续深挖还是切换方向。\n\n")
	}

	return sb.String()
}

// buildObjectiveViewSection 呈现任务目标与派生目标。进行中的派生目标带 ID——
// 它们是 completed_objectives 与 supersedes 唯一可引用的对象；已收敛的不带 ID，
// 只用于避免重复提出同一方向。
func buildObjectiveViewSection(view objectiveView) string {
	var sb strings.Builder
	sb.WriteString("## 任务目标\n\n")
	if view.task != "" {
		fmt.Fprintf(&sb, "%s\n\n", view.task)
	} else {
		sb.WriteString("（未指定明确目标）\n\n")
	}

	sb.WriteString("## 进行中的派生目标\n\n")
	sb.WriteString("探索中提出、尚在推进的方向。`completed_objectives` 与 `supersedes` 只能引用这里的 ID：\n\n")
	if len(view.live) == 0 {
		sb.WriteString("（无）\n")
	}
	for _, obj := range view.live {
		fmt.Fprintf(&sb, "- `%s` %s\n", obj.ID, objectiveDescription(obj))
	}
	sb.WriteString("\n")

	if len(view.settled) > 0 {
		sb.WriteString("## 已收敛的派生目标\n\n")
		sb.WriteString("已达成或已被吸收的方向，不要重复提出：\n\n")
		for _, obj := range view.settled {
			fmt.Fprintf(&sb, "- [%s] %s\n", settledLabel(obj), objectiveDescription(obj))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// buildAnalysisResultsSection 构建本次分析对象（新坐实、尚未分析的 Results）的描述
func buildAnalysisResultsSection(results []explorationgraph.Node) string {
	var sb strings.Builder
	sb.WriteString("## 新坐实的 Results（本次分析对象）\n\n")

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

	// 1. 达成判断：只针对派生目标；任务何时结束由探索收敛决定
	sb.WriteString("### 1. 哪些进行中的派生目标已达成？\n")
	sb.WriteString("- 该方向期望的成果已经拿到，且上方 Results 中有直接证据\n")
	sb.WriteString("- 如果是 → 在 `completed_objectives` 中给出该目标 ID 与 `evidence_ids`（必须是上方 Result 的 ID）\n")
	sb.WriteString("- 任务目标不在此列：任务何时结束由整体探索收敛决定，不由单次分析宣告\n\n")

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
	sb.WriteString("- **或** 任务的 Actions 已经很多（>20 个），但目标未达成\n")
	sb.WriteString("- Results 暗示存在**本质不同**的探索面：\n")
	sb.WriteString("  - 不同入口点（如发现新路径、新接口、新功能模块）\n")
	sb.WriteString("  - 不同探索链（如从一种方法切换到另一种完全不同的方法）\n")
	sb.WriteString("  - 不同资源类型（如从一类资源切换到另一类资源）\n\n")
	sb.WriteString("new_objectives 示例：\n")
	sb.WriteString("- 错误：\"继续探索当前路径\"（这是延续，不是新方向）\n")
	sb.WriteString("- 正确：\"探索备用路径的可访问性\"（不同入口点）\n")
	sb.WriteString("- 正确：\"测试其他功能模块的可用性\"（不同探索链）\n\n")

	// 吸收：新方向取代进行中的派生目标，范围转交而非放弃
	sb.WriteString("#### 新方向吸收旧方向（supersedes）的判断：\n")
	sb.WriteString("- 新方向**取代**某个进行中的派生目标——把它细化成更聚焦的方向、拆成多个方向，或依据新证据纠正它 → 在新方向的 `supersedes` 中填该目标 ID；拆分时每个新方向都填\n")
	sb.WriteString("- 被吸收目标的范围由新方向继续承担，不是放弃；方向已经达成用 `completed_objectives`，不用吸收\n")
	sb.WriteString("- 新方向与既有目标**范围不同**（不同入口/不同资产）→ 不填，两者并行推进\n")
	sb.WriteString("- 只能引用「进行中的派生目标」中的 ID；任务目标不可被吸收\n\n")

	// new_actions 条件
	sb.WriteString("#### 生成 new_actions（当前方向延续）的条件：\n")
	sb.WriteString("- 当前方向**未穷尽**\n")
	sb.WriteString("- Results 揭示了**当前范围内**的新探索点：\n")
	sb.WriteString("  - 新参数/字段需要探索\n")
	sb.WriteString("  - 需要尝试新方法变种\n")
	sb.WriteString("  - 发现可利用的细节\n\n")
	sb.WriteString("new_actions 示例：\n")
	sb.WriteString("- 正确：\"探索发现的新参数的可能值\"\n")
	sb.WriteString("- 正确：\"在特定字段尝试其他输入方法\"\n\n")

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
	sb.WriteString("  \"reasoning\": \"详细说明你的判断逻辑：哪些方向已达成？当前方向是否穷尽？为什么选择新方向/延续？\",\n")
	sb.WriteString("  \"completed_objectives\": [\n")
	sb.WriteString("    {\n")
	sb.WriteString("      \"id\": \"进行中的派生目标ID\",\n")
	sb.WriteString("      \"evidence_ids\": [\"result_id_1\"]\n")
	sb.WriteString("    }\n")
	sb.WriteString("  ],\n")
	sb.WriteString("  \"new_objectives\": [\n")
	sb.WriteString("    {\n")
	sb.WriteString("      \"description\": \"本质不同的新方向（不同入口/利用链/资产）\",\n")
	sb.WriteString("      \"priority\": \"high\",\n")
	sb.WriteString("      \"triggered_by\": [\"result_id_1\"],\n")
	sb.WriteString("      \"supersedes\": [\"进行中的派生目标ID\"],\n")
	sb.WriteString("      \"reasoning\": \"为什么需要这个新方向；若声明了 supersedes，说明它如何承接被吸收目标的范围\"\n")
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
	sb.WriteString("- 三类输出可以同时存在：宣告某方向达成、提出新方向、继续深挖当前方向\n")
	sb.WriteString("- 派生目标清单靠你收敛：方向达成就宣告 `completed_objectives`，细化/拆分/纠正既有方向就声明 `supersedes`\n")
	sb.WriteString("- 直接返回纯 JSON，不要用 Markdown 代码块标记\n")
	return sb.String()
}
