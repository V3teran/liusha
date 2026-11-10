package planner

import (
	"context"
	"fmt"
	"strings"

	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/framework/llm"
)

// AnalyzeResults 分析 Result 节点，决定下一步行动：哪些派生目标已达成、
// 是否提出新方向（及其吸收的旧方向）、任务方向上还要深挖什么。
func (e *Engine) AnalyzeResults(ctx context.Context, graph *explorationgraph.Store, taskID string, results []explorationgraph.Node) (*ResultAnalysis, error) {
	e.logger.Info().
		Int("result_count", len(results)).
		Msg("开始分析 Results")

	if len(results) == 0 {
		return &ResultAnalysis{}, nil
	}

	// 1. 目标视野：根目标（创建序最早）是任务意图；派生目标是修订的对象
	objectives, err := graph.ListNodesByKind(ctx, taskID, core.KindObjective)
	if err != nil {
		return nil, fmt.Errorf("获取 Objective 失败: %w", err)
	}
	if len(objectives) == 0 {
		return nil, fmt.Errorf("当前无 Objective 节点")
	}
	view := newObjectiveView(objectives)

	// 1.5. 任务动作总量（多样性判断；动作统一挂在根目标下）
	allActions, err := graph.ListNodesByKind(ctx, taskID, core.KindAction)
	if err != nil {
		return nil, fmt.Errorf("无法获取 Actions: %w", err)
	}
	actionCount := len(allActions)

	e.logger.Info().
		Int("total_actions", actionCount).
		Int("live_derived_objectives", len(view.live)).
		Int("settled_derived_objectives", len(view.settled)).
		Msg("当前探索状态统计")

	// 2. 构建分析 prompt
	prompt := e.buildAnalysisPrompt(view, results, actionCount)

	// 3. 调用 LLM
	response, err := e.callAnalysisLLM(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("LLM 分析失败: %w", err)
	}

	// 4. 转换响应为 ResultAnalysis
	analysis := &ResultAnalysis{Reasoning: response.Reasoning}
	for _, c := range response.CompletedObjectives {
		analysis.CompletedObjectives = append(analysis.CompletedObjectives, explorationgraph.ObjectiveCompletion{
			ObjectiveID: c.ID,
			Evidence:    c.EvidenceIDs,
		})
	}
	for _, obj := range response.NewObjectives {
		analysis.NewObjectives = append(analysis.NewObjectives, NewObjective{
			Description: obj.Description,
			Priority:    explorationgraph.NormalizePriority(obj.Priority),
			TriggeredBy: obj.TriggeredBy,
			Supersedes:  obj.Supersedes,
			Reasoning:   obj.Reasoning,
		})
	}
	for _, action := range response.NewActions {
		analysis.ContinuationActions = append(analysis.ContinuationActions, ContinuationAction{
			Instruction: action.Instruction,
			Priority:    explorationgraph.NormalizePriority(action.Priority),
			TriggeredBy: action.TriggeredBy,
			Reasoning:   action.Reasoning,
		})
	}

	return analysis, nil
}

// buildAnalysisPrompt 构建 Result 分析 prompt
func (e *Engine) buildAnalysisPrompt(view objectiveView, results []explorationgraph.Node, actionCount int) string {
	var sb strings.Builder

	// 1. 头部
	sb.WriteString(buildAnalysisHeader())

	// 2. Action 数量警告
	sb.WriteString(buildActionCountWarning(actionCount))

	// 3. 任务目标与派生目标（进行中的可修订；已收敛的供去重）
	sb.WriteString(buildObjectiveViewSection(view))

	// 4. 新 Results
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
	Reasoning           string                       `json:"reasoning"`
	CompletedObjectives []AnalysisCompletedObjective `json:"completed_objectives"`
	NewObjectives       []AnalysisObjective          `json:"new_objectives"`
	NewActions          []AnalysisAction             `json:"new_actions"`
}

// AnalysisCompletedObjective 是 AnalyzeResults 宣告达成的派生目标。
type AnalysisCompletedObjective struct {
	ID          string   `json:"id"`
	EvidenceIDs []string `json:"evidence_ids"`
}

// AnalysisObjective 是 AnalyzeResults 提议的新目标。
type AnalysisObjective struct {
	Description string   `json:"description"`
	Priority    string   `json:"priority"`
	TriggeredBy []string `json:"triggered_by"`
	Supersedes  []string `json:"supersedes"`
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
func (e *Engine) callAnalysisLLM(ctx context.Context, prompt string) (*AnalysisResponse, error) {
	provider, err := e.router.For(ctx, e.complexity)
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

	var analysis AnalysisResponse
	if err := llm.ParseJSON(resp.Content, &analysis); err != nil {
		e.logger.Error().
			Err(err).
			Str("raw_response", resp.Content).
			Msg("解析结果分析输出失败")
		return nil, fmt.Errorf("解析结果分析输出失败: %w", err)
	}

	return &analysis, nil
}
