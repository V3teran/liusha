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
func (i *Intelligence) AnalyzeResults(ctx context.Context, graph *explorationgraph.Store, taskID string, results []explorationgraph.Node) (*ResultAnalysis, error) {
	i.logger.Info().
		Str("task_id", taskID).
		Int("result_count", len(results)).
		Msg("开始分析 Results")

	if len(results) == 0 {
		return &ResultAnalysis{}, nil
	}

	// 1. 获取当前 Objective（同 planner/agent 口径：列表首个为当前根目标）
	objectives, err := graph.ListNodesByKind(ctx, taskID, core.KindObjective)
	if err != nil {
		return nil, fmt.Errorf("获取当前 Objective 失败: %w", err)
	}
	if len(objectives) == 0 {
		return nil, fmt.Errorf("当前无 Objective 节点")
	}
	currentObjective := objectives[0]

	// 1.5. 统计当前 Objective 下的 Actions 数量（用于多样性判断）
	allActions, err := graph.ListNodesByKind(ctx, taskID, core.KindAction)
	if err != nil {
		return nil, fmt.Errorf("无法获取 Actions: %w", err)
	}

	// 简化判断：统计所有 Actions（当前单 Objective；多 Objective 后需按边关系过滤）
	actionCount := len(allActions)

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
		analysis.NewObjectives = append(analysis.NewObjectives, NewObjective{
			Description: obj.Description,
			Priority:    explorationgraph.NormalizePriority(obj.Priority),
			TriggeredBy: obj.TriggeredBy,
			Reasoning:   obj.Reasoning,
		})
	}

	// 转换 ContinuationActions
	for _, action := range response.ContinuationActions {
		analysis.ContinuationActions = append(analysis.ContinuationActions, ContinuationAction{
			Instruction: action.Instruction,
			Priority:    explorationgraph.NormalizePriority(action.Priority),
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
	
	// 1. 头部
	sb.WriteString(buildAnalysisHeader())
	
	// 2. Action 数量警告
	sb.WriteString(buildActionCountWarning(actionCount))
	
	// 3. 当前 Objective
	sb.WriteString(buildCurrentObjectiveSection(currentObjective))
	
	// 4. 已有 Results
	sb.WriteString(buildAnalysisResultsSection(results))
	
	// 5. 决策流程指导
	sb.WriteString(buildDecisionGuide())
	
	// 6. 响应格式
	sb.WriteString(buildResponseFormat())
	
	// 7. 关键提示
	sb.WriteString(buildKeyReminders())
	
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

// AnalysisObjective 是 AnalyzeResults 提议的新目标。
type AnalysisObjective struct {
	Description string   `json:"description"`
	Priority    string   `json:"priority"`
	TriggeredBy []string `json:"triggered_by"`
	Reasoning   string   `json:"reasoning"`
}

// AnalysisAction 是 AnalyzeResults 提议的新动作。
type AnalysisAction struct {
	Instruction string   `json:"instruction"`
	Priority    string   `json:"priority"`
	TriggeredBy []string `json:"triggered_by"`
	Reasoning   string   `json:"reasoning"`
}

// callAnalysisLLM 调用 LLM 进行分析
func (i *Intelligence) callAnalysisLLM(ctx context.Context, prompt string) (*AnalysisResponse, error) {
	provider, err := i.router.For(ctx, i.complexity)
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

	// 截取 JSON 正文（剥掉 markdown 围栏与前后闲聊）
	content := llm.ExtractJSON(resp.Content)

	var analysis AnalysisResponse
	if err := json.Unmarshal([]byte(content), &analysis); err != nil {
		i.logger.Error().
			Err(err).
			Str("raw_response", resp.Content).
			Msg("解析 LLM 响应失败")
		return nil, fmt.Errorf("解析 JSON 失败: %w", err)
	}

	return &analysis, nil
}
