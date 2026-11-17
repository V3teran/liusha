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
// （objectives 按创建序升序）。
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

// objectiveDescription 返回目标描述（缺失时回落为 ID）。
func objectiveDescription(obj explorationgraph.Node) string {
	if goal := obj.Goal(); goal != "" {
		return goal
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
	if len(view.live) == 0 {
		sb.WriteString("（无）\n")
	}
	for _, obj := range view.live {
		fmt.Fprintf(&sb, "- `%s` %s\n", obj.ID, objectiveDescription(obj))
	}
	sb.WriteString("\n")

	if len(view.settled) > 0 {
		sb.WriteString("## 已收敛的派生目标\n\n")
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
