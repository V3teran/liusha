package planner

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/V3teran/liusha/internal/explorationgraph"
)

// objectiveView 是结果分析视角下的目标：任务目标（根目标）与已有的方向（派生目标）。
type objectiveView struct {
	task       string                  // 任务目标描述（根目标）
	directions []explorationgraph.Node // 已有的方向，创建序：新方向不与它们重复
}

// newObjectiveView 划分根目标与派生目标（objectives 按创建序升序）。
func newObjectiveView(objectives []explorationgraph.Node) objectiveView {
	var view objectiveView
	if root, ok := explorationgraph.RootObjective(objectives); ok {
		view.task = objectiveDescription(root)
	}
	for _, obj := range objectives {
		if obj.IsDerivedObjective() {
			view.directions = append(view.directions, obj)
		}
	}
	return view
}

// objectiveDescription 返回目标描述（缺失时回落为 ID）。
func objectiveDescription(obj explorationgraph.Node) string {
	if goal := obj.Goal(); goal != "" {
		return goal
	}
	return obj.ID
}

// buildObjectiveViewSection 呈现任务目标与已有的方向：分析据此决定要不要提出新方向，
// 新方向不与它们重复。
func buildObjectiveViewSection(view objectiveView) string {
	var sb strings.Builder
	sb.WriteString("## 任务目标\n\n")
	if view.task != "" {
		fmt.Fprintf(&sb, "%s\n\n", view.task)
	} else {
		sb.WriteString("（未指定明确目标）\n\n")
	}

	sb.WriteString("## 已有的方向\n\n")
	if len(view.directions) == 0 {
		sb.WriteString("（无）\n")
	}
	for _, obj := range view.directions {
		fmt.Fprintf(&sb, "- %s\n", objectiveDescription(obj))
	}
	sb.WriteString("\n")
	return sb.String()
}

// buildAnalysisResultsSection 构建本次分析对象（新坐实、尚未分析的 Results）的描述
func buildAnalysisResultsSection(results []explorationgraph.Node) string {
	var sb strings.Builder
	sb.WriteString("## 本次分析的新结果\n\n")

	for i, result := range results {
		var content map[string]interface{}
		if err := json.Unmarshal(result.Content, &content); err != nil {
			fmt.Fprintf(&sb, "%d. Result ID: %s (解析失败: %v)\n", i+1, result.ID, err)
			continue
		}
		fmt.Fprintf(&sb, "%d. Result ID: %s\n", i+1, result.ID)
		if statement, ok := content["statement"].(string); ok {
			fmt.Fprintf(&sb, "   内容：%s\n", statement)
		}
		sb.WriteString("\n")
	}

	return sb.String()
}
