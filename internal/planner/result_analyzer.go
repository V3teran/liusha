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

	// 2. 构建分析 prompt
	prompt := i.buildAnalysisPrompt(currentObjective, results)

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
func (i *Intelligence) buildAnalysisPrompt(currentObjective explorationgraph.Node, results []explorationgraph.Node) string {
	var sb strings.Builder

	sb.WriteString("你是探索分析专家，需要分析当前的探索结果并决定下一步行动。\n\n")

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
	sb.WriteString("## 分析任务\n\n")
	sb.WriteString("根据这些 Results，你需要判断：\n\n")

	sb.WriteString("### 1. 当前 Objective 是否已完成？\n")
	sb.WriteString("- 检查 Objective 的目标是否已达成\n")
	sb.WriteString("- 这些 Results 是否提供了足够的证据？\n")
	sb.WriteString("- 如果是 → 设置 completed=true，提供 evidence_ids\n\n")

	sb.WriteString("### 2. 是否发现了新的探索方向？\n")
	sb.WriteString("- 新方向指：不同的攻击面/入口点/资产\n")
	sb.WriteString("- **不是**当前 Objective 范围内的延续\n")
	sb.WriteString("- 例子：发现了新的目录、新的认证机制、不同类型的漏洞\n")
	sb.WriteString("- 如果是 → 生成 new_objectives\n\n")

	sb.WriteString("### 3. 是否在当前方向发现了新测试点？\n")
	sb.WriteString("- **仍在**当前 Objective 的范围内\n")
	sb.WriteString("- 只是发现了新的可测试点/参数/payload\n")
	sb.WriteString("- 例子：发现了新参数、需要测试不同 payload\n")
	sb.WriteString("- 如果是 → 生成 new_actions\n\n")

	// 响应格式
	sb.WriteString("## 响应格式\n\n")
	sb.WriteString("```json\n")
	sb.WriteString("{\n")
	sb.WriteString("  \"completed\": false,\n")
	sb.WriteString("  \"evidence_ids\": [\"result_id_1\"],  // completed=true 时必填\n")
	sb.WriteString("  \"reasoning\": \"你的判断理由\",\n")
	sb.WriteString("  \"new_objectives\": [  // 如果发现新方向\n")
	sb.WriteString("    {\n")
	sb.WriteString("      \"description\": \"新探索方向的描述\",\n")
	sb.WriteString("      \"priority\": \"high/medium/low\",\n")
	sb.WriteString("      \"triggered_by\": [\"result_id_1\"],\n")
	sb.WriteString("      \"reasoning\": \"为什么需要这个方向\"\n")
	sb.WriteString("    }\n")
	sb.WriteString("  ],\n")
	sb.WriteString("  \"new_actions\": [  // 如果是当前方向延续\n")
	sb.WriteString("    {\n")
	sb.WriteString("      \"instruction\": \"具体的测试指令\",\n")
	sb.WriteString("      \"priority\": \"high/medium/low\",\n")
	sb.WriteString("      \"triggered_by\": [\"result_id_1\"],\n")
	sb.WriteString("      \"reasoning\": \"为什么需要这个动作\"\n")
	sb.WriteString("    }\n")
	sb.WriteString("  ]\n")
	sb.WriteString("}\n")
	sb.WriteString("```\n\n")

	sb.WriteString("**重要**：\n")
	sb.WriteString("- completed=true 时，必须提供 evidence_ids\n")
	sb.WriteString("- new_objectives 用于新方向，new_actions 用于当前方向延续\n")
	sb.WriteString("- 二者可以同时存在，但要区分清楚\n\n")

	sb.WriteString("**响应格式要求**：\n")
	sb.WriteString("请直接返回 JSON 对象，不要使用 Markdown 代码块标记（```json 或 ```）。\n")
	sb.WriteString("直接输出纯 JSON，如：\n")
	sb.WriteString("{\"completed\":false,\"reasoning\":\"...\",\"new_objectives\":[...]}\n")

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
