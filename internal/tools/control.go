package tools

import (
	"context"
	"encoding/json"

	"github.com/V3teran/liusha/internal/registry"
)

// ─── done ───────────────────────────────────────────────────────────────────

var doneSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "reason":  {"type": "string", "description": "一句话终止原因，概括本次 Move 的结论。"},
    "summary": {"type": "string", "description": "详细结论，供上层 Dispatcher 汇总。"}
  },
  "required": ["reason"]
}`)

type doneTool struct{}

func (doneTool) Name() string      { return "done" }
func (doneTool) ShortDesc() string { return "终止当前任务收尾" }
func (doneTool) Desc() string {
	return "终止当前任务收尾，可带 reason/summary 供检查器与报告参考。"
}
func (doneTool) Schema() json.RawMessage { return doneSchema }

func (doneTool) Execute(_ context.Context, args json.RawMessage) (registry.ToolResult, error) {
	var a struct {
		Reason  string `json:"reason"`
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return registry.ToolResult{Error: "done: 解析参数失败: " + err.Error()}, nil
	}
	out := "done: " + a.Reason
	if a.Summary != "" {
		out += "\n" + a.Summary
	}
	return registry.ToolResult{Output: out}, nil
}

// ─── mark_insight ────────────────────────────────────────────────────────────

var markInsightSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "label":  {"type": "string", "description": "节点标签，简短词组，如「发现越权入口」。"},
    "detail": {"type": "string", "description": "补充说明（可选）。"}
  },
  "required": ["label"]
}`)

type markInsightTool struct{}

func (markInsightTool) Name() string      { return "mark_insight" }
func (markInsightTool) ShortDesc() string { return "标记关键节点" }
func (markInsightTool) Desc() string {
	return "在执行图上标记关键节点（判断/发现），帮观察者看懂调查思路，不进黑板。"
}
func (markInsightTool) Schema() json.RawMessage { return markInsightSchema }

func (markInsightTool) Execute(_ context.Context, args json.RawMessage) (registry.ToolResult, error) {
	var a struct {
		Label  string `json:"label"`
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return registry.ToolResult{Error: "mark_insight: 解析参数失败: " + err.Error()}, nil
	}
	out := "insight marked: " + a.Label
	if a.Detail != "" {
		out += "\n" + a.Detail
	}
	return registry.ToolResult{Output: out}, nil
}
