package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/V3teran/liusha/internal/provider"
)

// selfEvaluate 执行自我评估，判断是否跑偏。
func (a *Agent) selfEvaluate(ctx context.Context, goal string, recentSteps []Step) (SelfAssessment, error) {
	if len(recentSteps) == 0 {
		return SelfAssessment{Status: "on_track", Severity: "low"}, nil
	}

	// 构造评估 prompt
	prompt := buildSelfEvaluationPrompt(goal, recentSteps)

	// 调用 LLM
	resp, err := a.monitorProvider.Complete(ctx, provider.Request{
		Messages: []provider.Message{
			{Role: "user", Content: prompt},
		},
		MaxTokens: 500,
	})
	if err != nil {
		// 评估失败不影响执行，返回默认值
		return SelfAssessment{Status: "on_track", Severity: "low"}, err
	}

	// 解析评估结果
	return parseSelfAssessment(resp.Content), nil
}

// buildSelfEvaluationPrompt 构造自我评估 prompt。
func buildSelfEvaluationPrompt(goal string, recentSteps []Step) string {
	var stepsDesc strings.Builder
	for i, step := range recentSteps {
		if step.Thought != "" {
			stepsDesc.WriteString(fmt.Sprintf("%d. Thought: %s\n", i+1, step.Thought))
		}
		for _, tc := range step.ToolCalls {
			stepsDesc.WriteString(fmt.Sprintf("   Tool: %s(%s)\n", tc.Name, tc.Args))
		}
		for _, tr := range step.ToolResults {
			if tr.Error != "" {
				stepsDesc.WriteString(fmt.Sprintf("   Result: ERROR - %s\n", tr.Error))
			} else {
				// 截断过长的输出
				output := tr.Output
				if len(output) > 200 {
					output = output[:200] + "..."
				}
				stepsDesc.WriteString(fmt.Sprintf("   Result: %s\n", output))
			}
		}
	}

	return fmt.Sprintf(`You are a self-monitoring agent evaluating your own execution.

Your Goal: %s

Your Recent Steps:
%s

Self-Evaluation Questions:
1. Am I making progress toward the goal?
2. Am I on track or have I drifted off course?
3. Am I stuck (repeated failures, no progress)?
4. What is my current risk level?

Return JSON:
{
  "status": "on_track" | "off_track" | "stalled",
  "severity": "low" | "medium" | "high",
  "reasoning": "brief explanation",
  "correction": "if off_track, how to correct (optional)"
}`, goal, stepsDesc.String())
}

// parseSelfAssessment 解析 LLM 返回的评估结果。
func parseSelfAssessment(content string) SelfAssessment {
	var assessment SelfAssessment

	// 尝试解析 JSON
	if err := json.Unmarshal([]byte(content), &assessment); err != nil {
		// JSON 解析失败，使用默认值
		return SelfAssessment{
			Status:    "on_track",
			Severity:  "low",
			Reasoning: "Failed to parse assessment",
		}
	}

	// 验证字段
	if assessment.Status == "" {
		assessment.Status = "on_track"
	}
	if assessment.Severity == "" {
		assessment.Severity = "low"
	}

	return assessment
}
