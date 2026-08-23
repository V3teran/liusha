package tools

import (
	"context"
	"encoding/json"
	"fmt"

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
func (t *writeLeadTool) ShortDesc() string       { return "写一条跨 agent 情报" }
func (t *writeLeadTool) Desc() string {
	return "写一条跨 agent 情报到情报黑板（按 host 共享给子代理/跨 run，不进交付报告）。"
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

	entry := lead.Entry{
		Kind:         lead.Kind(a.Kind),
		Detail:       a.Detail,
		ExecutorID:   t.deps.ExecutorID,
		SourceTaskID: t.deps.TaskID,
	}
	if err := t.deps.Leads.Append(ctx, t.deps.Host, entry); err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("write_lead: %v", err)}, nil
	}
	return registry.ToolResult{Output: fmt.Sprintf("情报已写入黑板: [%s] %s", a.Kind, a.Detail)}, nil
}
