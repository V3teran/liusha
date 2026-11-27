package planner

import (
	"context"
	"fmt"
	"strings"

	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/framework/llm"
	plannercontract "github.com/V3teran/liusha/internal/planner/contract"
	"github.com/V3teran/liusha/internal/scenario"
)

// AnalyzeResults 分析 Result 节点，决定要不要提出新方向。下一步做什么由随后的常规规划
// 决定。
func (e *Engine) AnalyzeResults(ctx context.Context, graph *explorationgraph.Store, taskID string, results []explorationgraph.Node) (*ResultAnalysis, error) {
	e.logger.Info().
		Int("result_count", len(results)).
		Msg("开始分析 Results")

	if len(results) == 0 {
		return &ResultAnalysis{}, nil
	}

	// 1. 目标视野：根目标（创建序最早）是任务意图；派生目标是已有的方向
	objectives, err := graph.ListNodesByKind(ctx, taskID, core.KindObjective)
	if err != nil {
		return nil, fmt.Errorf("获取 Objective 失败: %w", err)
	}
	if len(objectives) == 0 {
		return nil, fmt.Errorf("当前无 Objective 节点")
	}
	view := newObjectiveView(objectives)

	// 1.5. 任务动作总量（多样性判断；按任务计，不分方向）
	allActions, err := graph.ListNodesByKind(ctx, taskID, core.KindAction)
	if err != nil {
		return nil, fmt.Errorf("无法获取 Actions: %w", err)
	}
	actionCount := len(allActions)

	e.logger.Info().
		Int("total_actions", actionCount).
		Int("directions", len(view.directions)).
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
	for _, obj := range response.NewObjectives {
		analysis.NewObjectives = append(analysis.NewObjectives, NewObjective{
			Description: obj.Description,
			Priority:    explorationgraph.NormalizePriority(obj.Priority),
			TriggeredBy: obj.TriggeredBy,
			Reasoning:   obj.Reasoning,
		})
	}

	return analysis, nil
}

// buildAnalysisPrompt 构建结果分析的输入：只陈述数据——任务的动作总量、目标视野与本次
// 分析的新结果。怎样判断要不要提出新方向由章程说明，响应格式由结果分析契约规定。
func (e *Engine) buildAnalysisPrompt(view objectiveView, results []explorationgraph.Node, actionCount int) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "任务目前共有 %d 个动作。\n\n", actionCount)
	sb.WriteString(buildObjectiveViewSection(view))
	sb.WriteString(buildAnalysisResultsSection(results))
	return sb.String()
}

// buildAnalysisSystemPrompt 组装结果分析的 system prompt：规划者章程（可自定义）+ 结果分析
// 契约（planner/contract，与本文件的解析同改同测，不可自定义）+ 任务场景块（无场景时没有）。
// 结果分析是规划者的另一项职责，与常规规划共用同一份章程。章程为空时返回空串，由调用方
// 拒绝。
func (e *Engine) buildAnalysisSystemPrompt() string {
	charter := strings.TrimSpace(e.charter)
	if charter == "" {
		return ""
	}
	return scenario.Append(charter+"\n\n"+plannercontract.AnalysisText, e.scenario)
}

// AnalysisResponse 是 LLM 的分析响应
type AnalysisResponse struct {
	Reasoning     string              `json:"reasoning"`
	NewObjectives []AnalysisObjective `json:"new_objectives"`
}

// AnalysisObjective 是 AnalyzeResults 提议的新方向。
type AnalysisObjective struct {
	Description string   `json:"description"`
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

	system := e.buildAnalysisSystemPrompt()
	if system == "" {
		return nil, fmt.Errorf("规划者章程为空")
	}
	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: system},
		{Role: llm.RoleUser, Content: prompt},
	}

	resp, err := provider.Complete(ctx, llm.Request{
		Messages: messages,
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
