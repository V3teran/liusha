package planner

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/framework/llm"
)

// AnalyzeResults 分析 Result 节点，决定下一步行动
func (i *Intelligence) AnalyzeResults(ctx context.Context, world *explorationgraph.Store, taskID string, results []explorationgraph.Node) (*ResultAnalysis, error) {
	i.logger.Info().
		Str("task_id", taskID).
		Int("result_count", len(results)).
		Msg("开始分析 Results")

	if len(results) == 0 {
		return &ResultAnalysis{}, nil
	}

	// 1. 获取当前 Objective
	objectives, err := world.ListNodesByKind(ctx, taskID, core.KindObjective)
	if err != nil || len(objectives) == 0 {
		return nil, fmt.Errorf("无法获取当前 Objective")
	}
	currentObjective := objectives[len(objectives)-1]

	// 1.5. 统计当前 Objective 下的 Actions 数量（用于多样性判断）
	allActions, err := world.ListNodesByKind(ctx, taskID, core.KindAction)
	if err != nil {
		return nil, fmt.Errorf("无法获取 Actions: %w", err)
	}

	// 统计属于当前 Objective 的 Actions
	actionCount := 0
	for range allActions {
		// 简化判断：统计所有 Actions（因为目前只有一个 Objective）
		// 未来如果有多个 Objectives，需要通过边关系判断
		actionCount++
	}

	i.logger.Info().
		Int("total_actions", actionCount).
		Str("current_objective_id", currentObjective.ID).
		Msg("当前探索状态统计")

	// 2. 构建分析 prompt（传入 actionCount 作为上下文）
	prompt := i.buildAnalysisPrompt(currentObjective, results, actionCount)

	// 3. 调用 LLM
	response, err := i.callAnalysisLLM(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("LLM 分析失败: %w", err)
	}

	// 4. 转换响应为 ResultAnalysis
	analysis := &ResultAnalysis{
		Completed:           response.Completed,
		Evidence:            response.EvidenceIDs,
		NewObjectives:       make([]NewObjective, 0),
		ContinuationActions: make([]ContinuationAction, 0),
		Reasoning:           response.Reasoning,
	}

	// 转换 NewObjectives
	for _, obj := range response.NewObjectives {
		priority := core.PriorityMedium
		switch obj.Priority {
		case "critical":
			priority = core.PriorityCritical
		case "high":
			priority = core.PriorityHigh
		case "low":
			priority = core.PriorityLow
		}

		analysis.NewObjectives = append(analysis.NewObjectives, NewObjective{
			Description: obj.Description,
			Priority:    priority,
			TriggeredBy: obj.TriggeredBy,
			Reasoning:   obj.Reasoning,
		})
	}

	// 转换 ContinuationActions
	for _, action := range response.ContinuationActions {
		priority := core.PriorityMedium
		switch action.Priority {
		case "critical":
			priority = core.PriorityCritical
		case "high":
			priority = core.PriorityHigh
		case "low":
			priority = core.PriorityLow
		}

		analysis.ContinuationActions = append(analysis.ContinuationActions, ContinuationAction{
			Instruction: action.Instruction,
			Priority:    priority,
			TriggeredBy: action.TriggeredBy,
			Reasoning:   action.Reasoning,
		})
	}

	i.logger.Info().
		Bool("completed", analysis.Completed).
		Int("new_objectives", len(analysis.NewObjectives)).
		Int("new_actions", len(analysis.ContinuationActions)).
		Msg("Result 分析完成")

	return analysis, nil
}

// buildAnalysisPrompt 构建 Result 分析 prompt
func (i *Intelligence) buildAnalysisPrompt(currentObjective explorationgraph.Node, results []explorationgraph.Node, actionCount int) string {
	var sb strings.Builder

	sb.WriteString("你是探索系统的分析专家。你的职责：分析探索结果，判断当前方向是否穷尽，决定是继续深挖还是切换方向。\n\n")

	sb.WriteString("⚠️ **核心原则：保持探索路线多样性，避免单一方向过载**\n\n")
	sb.WriteString(fmt.Sprintf("**当前探索状态**：当前 Objective 下已有 **%d 个 Actions**。\n", actionCount))
	if actionCount > 50 {
		sb.WriteString("⚠️ **警告：Actions 数量过多（>50）**，当前方向很可能已穷尽或陷入无效循环。**强烈建议生成 new_objectives（切换到新方向）**。\n\n")
	} else if actionCount > 20 {
		sb.WriteString("⚠️ **注意：Actions 数量较多（>20）**，如果目标仍未达成，**倾向于生成 new_objectives（切换到新方向）**。\n\n")
	} else {
		sb.WriteString("当前方向探索适中，可根据 Results 内容决定是继续深挖还是切换方向。\n\n")
	}

	// 当前 Objective
	sb.WriteString("## 当前 Objective\n\n")
	var objContent map[string]interface{}
	json.Unmarshal(currentObjective.Content, &objContent)
	if desc, ok := objContent["description"].(string); ok {
		sb.WriteString(fmt.Sprintf("目标：%s\n\n", desc))
	}

	// Results
	sb.WriteString("## 已有的 Results\n\n")
	for i, result := range results {
		var content map[string]interface{}
		json.Unmarshal(result.Content, &content)
		sb.WriteString(fmt.Sprintf("%d. Result ID: %s\n", i+1, result.ID))
		if summary, ok := content["summary"].(string); ok {
			sb.WriteString(fmt.Sprintf("   内容：%s\n", summary))
		}
		sb.WriteString("\n")
	}

	// 分析指导
	sb.WriteString("## 决策流程\n\n")

	sb.WriteString("### 1. 当前 Objective 是否已完成？\n")
	sb.WriteString("- 目标已真正达成（发现目标漏洞 / 获得目标成果）\n")
	sb.WriteString("- Results 提供了充分证据\n")
	sb.WriteString("- 如果是 → 设置 `completed=true`，提供 `evidence_ids`\n\n")

	sb.WriteString("### 2. 当前方向是否已穷尽？（关键判断）\n")
	sb.WriteString("判断标准：\n")
	sb.WriteString("- **已尝试所有明显探索点**：该入口/路径/节点的常见方法已全部尝试\n")
	sb.WriteString("- **出现重复失败模式**：多次尝试同类方法均失败，无新思路\n")
	sb.WriteString("- **遇到硬性阻塞**：权限限制、资源不可达等无法绕过的障碍\n")
	sb.WriteString("- **Results 暗示方向错误**：反馈表明该路径不可行\n\n")

	sb.WriteString("**如果当前方向已穷尽** → 优先生成 `new_objectives`（切换到本质不同的新方向）\n\n")

	sb.WriteString("### 3. 新方向 vs 当前方向延续（决策分支）\n\n")

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

	sb.WriteString("#### 生成 new_actions（当前方向延续）的条件：\n")
	sb.WriteString("- 当前方向**未穷尽**\n")
	sb.WriteString("- Results 揭示了**当前范围内**的新探索点：\n")
	sb.WriteString("  - 新参数/字段需要探索\n")
	sb.WriteString("  - 需要尝试新方法变种\n")
	sb.WriteString("  - 发现可利用的细节\n\n")

	sb.WriteString("new_actions 示例：\n")
	sb.WriteString("- ✅ 正确：\"探索发现的新参数的可能值\"\n")
	sb.WriteString("- ✅ 正确：\"在特定字段尝试其他输入方法\"\n\n")

	sb.WriteString("### 4. 什么都不生成也是正常的\n")
	sb.WriteString("- 当前方向已穷尽，但暂时找不到新方向 → 空数组\n")
	sb.WriteString("- Results 不包含可操作信息 → 空数组\n\n")

	// 响应格式
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

	sb.WriteString("**关键提示**：\n")
	sb.WriteString("- 当前方向 Actions 很多但未成功时，**大概率应该切换方向（生成 new_objectives）**\n")
	sb.WriteString("- new_objectives 和 new_actions 的区别：前者是「本质不同的方向」，后者是「当前方向的深挖」\n")
	sb.WriteString("- 两者可以同时存在：当前方向还能挖一点，但同时发现了新方向\n")
	sb.WriteString("- 直接返回纯 JSON，不要用 Markdown 代码块标记\n")

	return sb.String()
}

// AnalysisResponse 是 LLM 的分析响应
type AnalysisResponse struct {
	Completed           bool                `json:"completed"`
	EvidenceIDs         []string            `json:"evidence_ids"`
	Reasoning           string              `json:"reasoning"`
	NewObjectives       []AnalysisObjective `json:"new_objectives"`
	ContinuationActions []AnalysisAction    `json:"new_actions"`
}

type AnalysisObjective struct {
	Description string   `json:"description"`
	Priority    string   `json:"priority"`
	TriggeredBy []string `json:"triggered_by"`
	Reasoning   string   `json:"reasoning"`
}

type AnalysisAction struct {
	Instruction string   `json:"instruction"`
	Priority    string   `json:"priority"`
	TriggeredBy []string `json:"triggered_by"`
	Reasoning   string   `json:"reasoning"`
}

// callAnalysisLLM 调用 LLM 进行分析
func (i *Intelligence) callAnalysisLLM(ctx context.Context, prompt string) (*AnalysisResponse, error) {
	provider, err := i.router.For(ctx, "medium")
	if err != nil {
		return nil, fmt.Errorf("获取 LLM provider 失败: %w", err)
	}

	messages := []llm.Message{
		{
			Role:    llm.RoleUser,
			Content: prompt,
		},
	}

	resp, err := provider.Complete(ctx, llm.Request{
		Messages:  messages,
		MaxTokens: 4000,
	})
	if err != nil {
		return nil, err
	}

	// 清理响应内容（移除 Markdown 代码块）
	content := resp.Content
	content = strings.TrimSpace(content)

	// 如果包含 Markdown 代码块标记，提取其中的 JSON
	if strings.Contains(content, "```") {
		lines := strings.Split(content, "\n")
		var jsonLines []string
		inBlock := false
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "```") {
				inBlock = !inBlock
				continue
			}
			if inBlock || (!strings.HasPrefix(content, "```") && !inBlock) {
				jsonLines = append(jsonLines, line)
			}
		}
		if len(jsonLines) > 0 {
			content = strings.Join(jsonLines, "\n")
		}
	}

	// 解析 JSON
	var analysis AnalysisResponse
	if err := json.Unmarshal([]byte(content), &analysis); err != nil {
		i.logger.Error().
			Err(err).
			Str("raw_response", resp.Content).
			Str("cleaned_content", content).
			Msg("解析 LLM 响应失败")
		return nil, fmt.Errorf("解析 JSON 失败: %w", err)
	}

	return &analysis, nil
}
