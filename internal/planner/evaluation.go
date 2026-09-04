package planner

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/V3teran/liusha/internal/provider"
	"github.com/V3teran/liusha/internal/worldmodel"
)

// GlobalState 是全局状态快照。
type GlobalState struct {
	TaskID    string
	Objective string
	Actions   []ActionState
	Findings  []string
}

// ActionState 是单个 action 的状态。
type ActionState struct {
	ID        string
	Goal      string
	State     string // open/running/done/aborted/...
	Priority  int
	DependsOn []string
}

// GlobalAssessment 是全局评估结果。
type GlobalAssessment struct {
	Strategy       string // "continue" | "adjust" | "replanning"
	Reasoning      string
	NewActions     []NewAction       // 需要生成的新 action
	ActionsToSteer map[string]string // actionID -> guidance
	ActionsToKill  []string          // 需要终止的 action ID
}

// NewAction 是待生成的 action。
type NewAction struct {
	Goal      string
	Priority  int
	DependsOn []string
}

// getGlobalState 从世界模型读取全局状态。
func (p *Agent) getGlobalState(ctx context.Context, taskID string) (GlobalState, error) {
	// 读取 Objective（从 KindObjective 节点）
	objectives, err := p.world.ListNodesByKind(ctx, taskID, "objective")
	if err != nil {
		return GlobalState{}, fmt.Errorf("list objectives: %w", err)
	}

	objective := ""
	if len(objectives) > 0 {
		// 尝试解析为结构化内容
		var objContent worldmodel.ObjectiveContent
		if err := json.Unmarshal(objectives[0].Content, &objContent); err == nil {
			objective = objContent.Description
			if objContent.Target != "" {
				objective += " (Target: " + objContent.Target + ")"
			}
		} else {
			// 回退：直接用字符串
			objective = string(objectives[0].Content)
		}
	}

	// 读取所有 action 节点
	actions, err := p.world.ListNodesByKind(ctx, taskID, "action")
	if err != nil {
		return GlobalState{}, fmt.Errorf("list actions: %w", err)
	}

	// 转换为 ActionState
	actionStates := make([]ActionState, 0, len(actions))
	for _, a := range actions {
		state := "unknown"
		if a.State != nil {
			state = string(*a.State)
		}

		// 解析 action content
		goal := string(a.Content)
		var actContent worldmodel.ActionContent
		if err := json.Unmarshal(a.Content, &actContent); err == nil {
			goal = actContent.Instruction
		}

		actionStates = append(actionStates, ActionState{
			ID:        a.ID,
			Goal:      goal,
			State:     state,
			Priority:  a.Priority,
			DependsOn: a.DependsOn,
		})
	}

	// 读取 findings
	findings, err := p.world.ListFindings(ctx, taskID)
	if err != nil {
		findings = nil // 忽略错误
	}

	findingStrs := make([]string, 0, len(findings))
	for _, f := range findings {
		// 解析 finding content
		var findContent worldmodel.FindingContent
		if err := json.Unmarshal(f.Content, &findContent); err == nil {
			findingStrs = append(findingStrs, fmt.Sprintf("[%s] %s", findContent.Severity, findContent.Title))
		} else {
			findingStrs = append(findingStrs, string(f.Content))
		}
	}

	return GlobalState{
		TaskID:    taskID,
		Objective: objective,
		Actions:   actionStates,
		Findings:  findingStrs,
	}, nil
}

// evaluateGlobal 执行全局评估。
func (p *Agent) evaluateGlobal(ctx context.Context, state GlobalState) (GlobalAssessment, error) {
	// 构造评估 prompt
	prompt := buildGlobalEvaluationPrompt(state)

	// 获取 Provider
	prov, err := p.router.For(ctx, provider.ComplexitySimple)
	if err != nil {
		return GlobalAssessment{}, err
	}

	// 调用 LLM
	resp, err := prov.Complete(ctx, provider.Request{
		Messages: []provider.Message{
			{Role: "user", Content: prompt},
		},
		MaxTokens: 1000,
	})
	if err != nil {
		return GlobalAssessment{}, err
	}

	// 解析评估结果
	return parseGlobalAssessment(resp.Content), nil
}

// buildGlobalEvaluationPrompt 构造全局评估 prompt。
func buildGlobalEvaluationPrompt(state GlobalState) string {
	var actionsDesc strings.Builder
	for _, a := range state.Actions {
		actionsDesc.WriteString(fmt.Sprintf("- [%s] %s (state: %s, priority: %d)\n",
			a.ID, a.Goal, a.State, a.Priority))
	}

	return fmt.Sprintf(`You are a global planner monitoring the overall execution of a penetration testing task.

Task Objective:
%s

Current Actions:
%s

Findings:
%s

Global Evaluation Questions:

1. Strategic Level:
   - Are all actions moving toward the objective?
   - Is the current strategy effective?
   - Is progress normal?

2. Coordination Level:
   - Is there duplicate work?
   - Are dependencies reasonable?
   - Do priorities need adjustment?

3. Resource Level:
   - Is resource allocation reasonable?
   - Are there deadlocks?
   - Are there bottlenecks?

Decisions (JSON):
{
  "strategy": "continue" | "adjust" | "replanning",
  "reasoning": "brief explanation",
  "new_actions": [
    {"goal": "...", "priority": 5, "depends_on": ["action_id"]}
  ],
  "actions_to_steer": {
    "action_id": "guidance message"
  },
  "actions_to_kill": ["action_id"]
}`, state.Objective, actionsDesc.String(), formatFindings(state.Findings))
}

func formatFindings(findings []string) string {
	if len(findings) == 0 {
		return "(none yet)"
	}
	return strings.Join(findings, "\n")
}

// parseGlobalAssessment 解析 LLM 返回的评估结果。
func parseGlobalAssessment(content string) GlobalAssessment {
	var assessment GlobalAssessment

	// 尝试解析 JSON
	if err := json.Unmarshal([]byte(content), &assessment); err != nil {
		// JSON 解析失败，返回默认值（继续当前策略）
		return GlobalAssessment{
			Strategy:  "continue",
			Reasoning: "Failed to parse assessment",
		}
	}

	// 验证字段
	if assessment.Strategy == "" {
		assessment.Strategy = "continue"
	}

	return assessment
}
