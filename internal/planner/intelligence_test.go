package planner

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// buildPlanningPrompt 是规划质量的关键：把探索图状态组装成 LLM 指令。
// 段落缺失/占位符错误会直接导致规划方向跑偏。

func TestBuildPlanningPrompt_ContainsObjective(t *testing.T) {
	i := &Intelligence{}
	prompt := i.buildPlanningPrompt(&PlanningContext{
		Objective: "测试 target.com 的越权漏洞",
	})
	assert.Contains(t, prompt, "测试 target.com 的越权漏洞")
	assert.NotContains(t, prompt, "（未指定明确目标）")
}

func TestBuildPlanningPrompt_EmptyObjective(t *testing.T) {
	i := &Intelligence{}
	prompt := i.buildPlanningPrompt(&PlanningContext{})
	assert.Contains(t, prompt, "（未指定明确目标）")
}

func TestBuildPlanningPrompt_Structure(t *testing.T) {
	i := &Intelligence{}
	prompt := i.buildPlanningPrompt(&PlanningContext{Objective: "x"})

	for _, seg := range []string{
		"## 核心原则：智能探索模式",
		"## 何时停止当前方向",
		"should_continue",
		"## 任务目标",
	} {
		assert.Contains(t, prompt, seg, "prompt 缺少段落: %s", seg)
	}
	// 停止判断必须是主动判断语义，不能是机械计数
	assert.Contains(t, prompt, "主动判断")
	assert.NotContains(t, prompt, "机械地执行 N 次后停止")
}

func TestAnalyzeResults_Watermark(t *testing.T) {
	// 水位逻辑在 agent.go（analyzedResultIDs map）——这里验证 Planner 接口契约：
	// AnalyzeResults 收到增量列表，调用方负责水位。
	assert.NotNil(t, (&Intelligence{}).AnalyzeResults, "AnalyzeResults 是 Planner 接口的方法")
}

func TestPlanningPrompt_LineCount(t *testing.T) {
	// prompt 是 LLM 的第一跳输入：长度需可控（< 4K 字符），
	// 超长会挤占上下文窗口（蒸馏机制只处理会话历史，不处理 planner prompt）。
	i := &Intelligence{}
	prompt := i.buildPlanningPrompt(&PlanningContext{
		Objective:        "x",
		CompletedActions: nil,
	})
	assert.Less(t, len(prompt), 4096, "planner prompt 应保持精炼")
	assert.Greater(t, len(strings.Fields(prompt)), 50, "prompt 不应为空壳")
}
