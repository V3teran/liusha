package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/V3teran/liusha/internal/registry"
	"github.com/V3teran/liusha/internal/traffic"
)

// ─── list_traffic ────────────────────────────────────────────────────────────

var listTrafficSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "source":      {"type": "string", "enum": ["agent", "proxy"], "description": "流量来源过滤（可选）：agent=主动扫描流量，proxy=被动代理抓包。"},
    "method":      {"type": "string", "description": "过滤 HTTP 方法（可选）。"},
    "path_prefix": {"type": "string", "description": "路径前缀过滤（可选），支持 * 通配。"},
    "status_min":  {"type": "integer", "description": "响应状态码下界（可选）。"},
    "status_max":  {"type": "integer", "description": "响应状态码上界（可选）。"},
    "identity":    {"type": "string", "description": "身份名过滤（可选，仅 agent 流量）。"},
    "tool":        {"type": "string", "description": "工具名过滤（可选，仅 agent 流量）。"},
    "limit":       {"type": "integer", "description": "最多返回条数，默认 20。"}
  }
}`)

type listTrafficTool struct {
	registry.BaseTool
	deps Deps
}

func newListTrafficTool(deps Deps, timeout time.Duration, safe bool) *listTrafficTool {
	t := &listTrafficTool{deps: deps}
	t.SetTimeout(timeout)
	t.SetConcurrencySafe(safe)
	return t
}

func (t *listTrafficTool) Name() string      { return "list_traffic" }
func (t *listTrafficTool) ShortDesc() string { return "列出本次扫描历史 HTTP 流量摘要" }
func (t *listTrafficTool) Desc() string {
	return "列出本次扫描范围内的历史 HTTP 流量摘要，可按 method/path/status 等过滤。"
}
func (t *listTrafficTool) Schema() json.RawMessage { return listTrafficSchema }

func (t *listTrafficTool) Execute(ctx context.Context, args json.RawMessage) (registry.ToolResult, error) {
	var a struct {
		Source     string `json:"source"`
		Method     string `json:"method"`
		PathPrefix string `json:"path_prefix"`
		StatusMin  int    `json:"status_min"`
		StatusMax  int    `json:"status_max"`
		Identity   string `json:"identity"`
		Tool       string `json:"tool"`
		Limit      int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("read_traffic: invalid args: %v", err)}, nil
	}
	if a.Limit <= 0 {
		a.Limit = 20
	}

	type row struct {
		ID         int64     `json:"id"`
		Source     string    `json:"source"`
		Method     string    `json:"method"`
		Path       string    `json:"path"`
		URL        string    `json:"url"`
		StatusCode int       `json:"status_code"`
		Identity   string    `json:"identity,omitempty"`
		Tool       string    `json:"tool,omitempty"`
		CreatedAt  time.Time `json:"created_at"`
	}

	var rows []row

	// agent traffic (只在 source="" 或 source="agent" 时查询)
	if t.deps.AgentStore != nil && (a.Source == "" || a.Source == "agent") {
		f := traffic.AgentListFilter{
			Method:    strings.ToUpper(a.Method),
			Path:      a.PathPrefix,
			Identity:  a.Identity,
			Tool:      a.Tool,
			StatusMin: a.StatusMin,
			StatusMax: a.StatusMax,
			Limit:     a.Limit,
		}
		list, err := t.deps.AgentStore.ListByTaskFiltered(ctx, t.deps.TaskID, f)
		if err == nil {
			for _, s := range list {
				rows = append(rows, row{
					ID: s.ID, Source: "agent",
					Method: s.Method, Path: s.Path, URL: s.URL,
					StatusCode: s.StatusCode, Identity: s.Identity, Tool: s.Tool,
					CreatedAt: s.CreatedAt,
				})
			}
		}
	}

	// proxy traffic (只在 source="" 或 source="proxy" 时查询)
	if t.deps.ProxyStore != nil && (a.Source == "" || a.Source == "proxy") {
		pf := traffic.ProxyListFilter{
			Method:    strings.ToUpper(a.Method),
			Path:      a.PathPrefix,
			StatusMin: a.StatusMin,
			StatusMax: a.StatusMax,
			Limit:     a.Limit,
		}
		list, err := t.deps.ProxyStore.ListByTaskFiltered(ctx, t.deps.TaskID, pf)
		if err == nil {
			for _, s := range list {
				rows = append(rows, row{
					ID: s.ID, Source: "proxy",
					Method: s.Method, Path: s.Path, URL: s.URL,
					StatusCode: s.StatusCode,
					CreatedAt:  s.CapturedAt,
				})
			}
		}
	}

	if len(rows) == 0 {
		return registry.ToolResult{Output: "[]"}, nil
	}
	b, _ := json.MarshalIndent(rows, "", "  ")
	return registry.ToolResult{Output: string(b)}, nil
}

// ─── view_traffic ────────────────────────────────────────────────────────────

var viewTrafficSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "id":     {"type": "integer", "description": "流量记录 ID（来自 list_traffic）。"},
    "source": {"type": "string", "enum": ["agent", "proxy"], "description": "流量来源，不填自动探测。"}
  },
  "required": ["id"]
}`)

type viewTrafficTool struct {
	registry.BaseTool
	deps Deps
}

func newViewTrafficTool(deps Deps, timeout time.Duration, safe bool) *viewTrafficTool {
	t := &viewTrafficTool{deps: deps}
	t.SetTimeout(timeout)
	t.SetConcurrencySafe(safe)
	return t
}

func (t *viewTrafficTool) Name() string      { return "view_traffic" }
func (t *viewTrafficTool) ShortDesc() string { return "查看单条历史流量完整内容" }
func (t *viewTrafficTool) Desc() string {
	return "取单条历史流量的完整 raw HTTP 请求+响应（headers/body 全量）。"
}
func (t *viewTrafficTool) Schema() json.RawMessage { return viewTrafficSchema }

func (t *viewTrafficTool) Execute(ctx context.Context, args json.RawMessage) (registry.ToolResult, error) {
	var a struct {
		ID     int64  `json:"id"`
		Source string `json:"source"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return registry.ToolResult{Error: "view_traffic: 解析参数失败: " + err.Error()}, nil
	}

	// try agent first, then proxy
	if a.Source != "proxy" && t.deps.AgentStore != nil {
		rec, err := t.deps.AgentStore.GetByID(ctx, a.ID)
		if err == nil {
			b, _ := json.MarshalIndent(rec, "", "  ")
			return registry.ToolResult{
				Output: string(b),
				Signal: &registry.Signal{Kind: registry.SignalHTTPTrace, ToolName: "view_traffic",
					Content: fmt.Sprintf("[agent] %s %s → %d", rec.Method, rec.URL, rec.StatusCode)},
			}, nil
		}
	}
	if a.Source != "agent" && t.deps.ProxyStore != nil {
		rec, err := t.deps.ProxyStore.GetByID(ctx, a.ID)
		if err == nil {
			type proxyView struct {
				ID          int64  `json:"id"`
				Method      string `json:"method"`
				URL         string `json:"url"`
				StatusCode  int    `json:"status_code"`
				RequestRaw  string `json:"request_raw"`
				ResponseRaw string `json:"response_raw"`
			}
			v := proxyView{
				ID: rec.ID, Method: rec.Method, URL: rec.URL, StatusCode: rec.StatusCode,
				RequestRaw:  string(rec.RequestRaw),
				ResponseRaw: string(rec.ResponseRaw),
			}
			b, _ := json.MarshalIndent(v, "", "  ")
			return registry.ToolResult{
				Output: string(b),
				Signal: &registry.Signal{Kind: registry.SignalHTTPTrace, ToolName: "view_traffic",
					Content: fmt.Sprintf("[proxy] %s %s → %d", rec.Method, rec.URL, rec.StatusCode)},
			}, nil
		}
	}
	return registry.ToolResult{Error: fmt.Sprintf("view_traffic: 未找到 id=%d 的流量记录", a.ID)}, nil
}

// ─── replay_traffic ──────────────────────────────────────────────────────────

var replayTrafficSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "id":     {"type": "integer", "description": "流量记录 ID（来自 list_traffic）。"},
    "source": {"type": "string", "enum": ["agent", "proxy"], "description": "流量来源，不填自动探测。"},
    "modifications": {
      "type": "object",
      "description": "字段级改写（未指定字段全部继承原请求）。",
      "properties": {
        "url":         {"type": "string"},
        "method":      {"type": "string"},
        "headers":     {"type": "object", "additionalProperties": {"type": ["string", "null"]}},
        "query":       {"type": "object", "additionalProperties": {"type": ["string", "null"]}},
        "body":        {"type": "string"},
        "body_fields": {"type": "object", "additionalProperties": {"type": ["string", "null"]}}
      }
    }
  },
  "required": ["id"]
}`)
