package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/V3teran/liusha/internal/registry"
)

// ─── read_findings ───────────────────────────────────────────────────────────

var readFindingsSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "host":  {"type": "string", "description": "目标 host（不填则用当前任务 host）。"},
    "limit": {"type": "integer", "description": "最多返回条数，默认 20。"}
  }
}`)

type readFindingsTool struct {
	registry.BaseTool
	deps Deps
}

func newReadFindingsTool(deps Deps, timeout time.Duration, safe bool) *readFindingsTool {
	t := &readFindingsTool{deps: deps}
	t.SetTimeout(timeout)
	t.SetConcurrencySafe(safe)
	return t
}

func (t *readFindingsTool) Name() string      { return "read_findings" }
func (t *readFindingsTool) ShortDesc() string { return "列出本次扫描已有 finding" }
func (t *readFindingsTool) Desc() string {
	return "列出本次扫描已有 finding（写前必查、防重复），返回 id/severity/summary 摘要。"
}
func (t *readFindingsTool) Schema() json.RawMessage { return readFindingsSchema }

func (t *readFindingsTool) Execute(ctx context.Context, args json.RawMessage) (registry.ToolResult, error) {
	var a struct {
		Host  string `json:"host"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("read_findings: invalid args: %v", err)}, nil
	}
	if a.Host == "" {
		a.Host = t.deps.Host
	}
	if a.Limit <= 0 {
		a.Limit = 20
	}

	list, err := t.deps.Findings.ListByTaskAndHost(ctx, t.deps.TaskID, a.Host, a.Limit)
	if err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("read_findings: %v", err)}, nil
	}

	type row struct {
		ID       string `json:"id"`
		Severity string `json:"severity"`
		Summary  string `json:"summary"`
		Status   string `json:"status"`
		Seq      int64  `json:"seq"`
	}
	rows := make([]row, 0, len(list))
	for _, f := range list {
		rows = append(rows, row{ID: f.ID, Severity: f.Severity, Summary: f.Summary, Status: f.Status, Seq: f.Seq})
	}
	b, _ := json.Marshal(rows)
	return registry.ToolResult{Output: string(b)}, nil
}

// ─── write_finding ───────────────────────────────────────────────────────────


// ─── update_finding ──────────────────────────────────────────────────────────

var updateFindingSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "id":       {"type": "string", "description": "Finding ID。"},
    "summary":  {"type": "string", "description": "更新 summary（可选）。"},
    "severity": {"type": "string", "description": "更新 severity（可选）。"},
    "evaluation": {"type": "object", "description": "更新 evaluation（可选）。"},
    "target":   {"type": "object", "description": "更新 target（可选）。"},
    "depends_on": {"type": "array", "items": {"type": "string"}}
  },
  "required": ["id"]
}`)

type updateFindingTool struct {
	registry.BaseTool
	deps Deps
}

func newUpdateFindingTool(deps Deps, timeout time.Duration, safe bool) *updateFindingTool {
	t := &updateFindingTool{deps: deps}
	t.SetTimeout(timeout)
	t.SetConcurrencySafe(safe)
	return t
}

func (t *updateFindingTool) Name() string      { return "update_finding" }
func (t *updateFindingTool) ShortDesc() string { return "更新已有 finding" }
func (t *updateFindingTool) Desc() string {
	return "更新已有 finding（保留首次发现时间，仅覆盖所传字段），用于补强 PoC/payload/severity。"
}
func (t *updateFindingTool) Schema() json.RawMessage { return updateFindingSchema }

func (t *updateFindingTool) Execute(ctx context.Context, args json.RawMessage) (registry.ToolResult, error) {
	var a struct {
		ID         string          `json:"id"`
		Summary    string          `json:"summary"`
		Severity   string          `json:"severity"`
		Evaluation json.RawMessage `json:"evaluation"`
		Target     json.RawMessage `json:"target"`
		DependsOn  []string        `json:"depends_on"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return registry.ToolResult{Error: "update_finding: 解析参数失败: " + err.Error()}, nil
	}
	if a.ID == "" {
		return registry.ToolResult{Error: "update_finding: id 必填"}, nil
	}

	if err := t.deps.Findings.Update(ctx, a.ID, a.Summary, a.Severity, a.Target, a.Evaluation, a.DependsOn); err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("update_finding: %v", err)}, nil
	}
	return registry.ToolResult{Output: fmt.Sprintf("finding 已更新: id=%s", a.ID)}, nil
}
