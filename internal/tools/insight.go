package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/V3teran/liusha/internal/insight"
	"github.com/V3teran/liusha/internal/registry"
)

var writeLeadSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "category": {
      "type": "string",
      "enum": ["target", "credential", "infrastructure", "business", "data", "result", "obstacle", "note"],
      "description": "信息分类：target（目标）/credential（凭证）/infrastructure（基础设施）/business（业务逻辑）/data（数据）/finding（发现）/obstacle（障碍）/note（笔记）"
    },
    "priority": {
      "type": "string",
      "enum": ["critical", "high", "medium", "low"],
      "default": "medium",
      "description": "优先级：critical（关键，P0）/high（高，P1）/medium（中，P2）/low（低，P3）"
    },
    "confidence": {
      "type": "string",
      "enum": ["possible", "probable", "confirmed"],
      "default": "possible",
      "description": "置信度：possible（可能）/probable（很可能）/confirmed（已确认）"
    },
    "summary": {
      "type": "string",
      "maxLength": 200,
      "description": "一句话摘要（必填，200 字符以内）"
    },
    "body": {
      "type": "string",
      "description": "详细内容（可选，markdown 格式）"
    },
    "tags": {
      "type": "array",
      "items": {"type": "string"},
      "description": "自由标签（可选）"
    }
  },
  "required": ["category", "summary"]
}`)

type writeInsightTool struct {
	registry.BaseTool
	deps Deps
}

func newWriteInsightTool(deps Deps, timeout time.Duration, safe bool) *writeInsightTool {
	t := &writeInsightTool{deps: deps}
	t.SetTimeout(timeout)
	t.SetConcurrencySafe(safe)
	return t
}

func (t *writeInsightTool) Name() string      { return "write_insight" }
func (t *writeInsightTool) ShortDesc() string { return "写一条跨 task 情报" }
func (t *writeInsightTool) Desc() string {
	return "写一条情报到 assignment 级别的黑板（同一批测试的多个 task 共享，跨 agent 可见）。"
}
func (t *writeInsightTool) Schema() json.RawMessage { return writeLeadSchema }

func (t *writeInsightTool) Execute(ctx context.Context, args json.RawMessage) (registry.ToolResult, error) {
	var a struct {
		Category   string   `json:"category"`
		Priority   string   `json:"priority"`
		Confidence string   `json:"confidence"`
		Summary    string   `json:"summary"`
		Body       string   `json:"body"`
		Tags       []string `json:"tags"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return registry.ToolResult{Error: "write_insight: 解析参数失败: " + err.Error()}, nil
	}
	if a.Summary == "" {
		return registry.ToolResult{Error: "write_insight: summary 必填"}, nil
	}
	if a.Category == "" {
		return registry.ToolResult{Error: "write_insight: category 必填"}, nil
	}

	// 默认值
	if a.Priority == "" {
		a.Priority = "medium"
	}
	if a.Confidence == "" {
		a.Confidence = "possible"
	}

	// 查询当前 task 所属的 assignment_id
	task, err := t.deps.Tasks.GetByID(ctx, t.deps.TaskID)
	if err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("write_insight: 查询 task 失败: %v", err)}, nil
	}

	entry := insight.Insight{
		Category:      insight.Category(a.Category),
		Priority:      insight.Priority(a.Priority),
		Confidence:    insight.Confidence(a.Confidence),
		Summary:       a.Summary,
		Body:          a.Body,
		Tags:          a.Tags,
		SourceTaskID:  t.deps.TaskID,
		SourceAgentID: t.deps.AgentID,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	if err := t.deps.Insights.Append(ctx, task.AssignmentID, entry); err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("write_insight: %v", err)}, nil
	}

	return registry.ToolResult{
		Output: fmt.Sprintf("情报已写入黑板: [%s/%s/%s] %s", a.Category, a.Priority, a.Confidence, a.Summary),
	}, nil
}
