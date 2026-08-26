package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/V3teran/liusha/internal/lead"
	"github.com/V3teran/liusha/internal/registry"
)

var writeLeadSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "kind":   {"type": "string", "enum": ["clue", "observation", "deadend"],
                "description": "clue=可疑点待验证 / observation=既成发现 / deadend=死路绕开。"},
    "detail": {"type": "string", "description": "一句人话，位置/细节都在这里说清。"}
  },
  "required": ["kind", "detail"]
}`)

type writeLeadTool struct{ deps Deps }

func (t *writeLeadTool) Name() string            { return "write_lead" }
func (t *writeLeadTool) ShortDesc() string       { return "写一条跨 task 情报" }
func (t *writeLeadTool) Desc() string {
	return "写一条情报到 assignment 级别的黑板（同一批测试的多个 task 共享，跨 agent 可见）。"
}
func (t *writeLeadTool) Schema() json.RawMessage { return writeLeadSchema }

func (t *writeLeadTool) Execute(ctx context.Context, args json.RawMessage) (registry.ToolResult, error) {
	var a struct {
		Kind   string `json:"kind"`
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return registry.ToolResult{Error: "write_lead: 解析参数失败: " + err.Error()}, nil
	}
	if a.Detail == "" {
		return registry.ToolResult{Error: "write_lead: detail 必填"}, nil
	}

	// 查询当前 task 所属的 assignment_id
	task, err := t.deps.Tasks.GetByID(ctx, t.deps.TaskID)
	if err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("write_lead: 查询 task 失败: %v", err)}, nil
	}

	entry := lead.Entry{
		Kind:         lead.Kind(a.Kind),
		Detail:       a.Detail,
		ExecutorID:   t.deps.ExecutorID,
		SourceTaskID: t.deps.TaskID,
		CreatedAt:    time.Now(),
	}

	if err := t.deps.Leads.Append(ctx, task.AssignmentID, entry); err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("write_lead: %v", err)}, nil
	}

	return registry.ToolResult{Output: fmt.Sprintf("情报已写入黑板: [%s] %s", a.Kind, a.Detail)}, nil
}
