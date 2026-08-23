package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/V3teran/liusha/internal/httpreplay"
	"github.com/V3teran/liusha/internal/registry"
	"github.com/V3teran/liusha/internal/traffic"
)

// ─── list_traffic ────────────────────────────────────────────────────────────

var listTrafficSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "method":      {"type": "string", "description": "过滤 HTTP 方法（可选）。"},
    "path_prefix": {"type": "string", "description": "路径前缀过滤（可选），支持 * 通配。"},
    "status_min":  {"type": "integer", "description": "响应状态码下界（可选）。"},
    "status_max":  {"type": "integer", "description": "响应状态码上界（可选）。"},
    "identity":    {"type": "string", "description": "身份名过滤（可选，仅 agent 流量）。"},
    "tool":        {"type": "string", "description": "工具名过滤（可选，仅 agent 流量）。"},
    "limit":       {"type": "integer", "description": "最多返回条数，默认 20。"}
  }
}`)

type listTrafficTool struct{ deps Deps }

func (t *listTrafficTool) Name() string      { return "list_traffic" }
func (t *listTrafficTool) ShortDesc() string { return "列出本次扫描历史 HTTP 流量摘要" }
func (t *listTrafficTool) Desc() string {
	return "列出本次扫描范围内的历史 HTTP 流量摘要，可按 method/path/status 等过滤。"
}
func (t *listTrafficTool) Schema() json.RawMessage { return listTrafficSchema }

func (t *listTrafficTool) Execute(ctx context.Context, args json.RawMessage) (registry.ToolResult, error) {
	var a struct {
		Method     string `json:"method"`
		PathPrefix string `json:"path_prefix"`
		StatusMin  int    `json:"status_min"`
		StatusMax  int    `json:"status_max"`
		Identity   string `json:"identity"`
		Tool       string `json:"tool"`
		Limit      int    `json:"limit"`
	}
	_ = json.Unmarshal(args, &a)
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

	// agent traffic
	if t.deps.AgentStore != nil {
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

	// proxy traffic
	if t.deps.ProxyStore != nil {
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

type viewTrafficTool struct{ deps Deps }

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

type replayTrafficTool struct{ deps Deps }

func (t *replayTrafficTool) Name() string      { return "replay_traffic" }
func (t *replayTrafficTool) ShortDesc() string { return "重发历史 HTTP 流量并可字段级改写" }
func (t *replayTrafficTool) Desc() string {
	return "重发历史 HTTP 流量并可字段级改写，做越权/未授权/IDOR/fuzz，自动保留 session 上下文。"
}
func (t *replayTrafficTool) Schema() json.RawMessage { return replayTrafficSchema }

func (t *replayTrafficTool) Execute(ctx context.Context, args json.RawMessage) (registry.ToolResult, error) {
	var a struct {
		ID            int64  `json:"id"`
		Source        string `json:"source"`
		Modifications *struct {
			URL        string                     `json:"url"`
			Method     string                     `json:"method"`
			Headers    map[string]*string         `json:"headers"`
			Query      map[string]*string         `json:"query"`
			Body       *string                    `json:"body"`
			BodyFields map[string]*string         `json:"body_fields"`
		} `json:"modifications"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return registry.ToolResult{Error: "replay_traffic: 解析参数失败: " + err.Error()}, nil
	}

	src, err := t.resolveSource(ctx, a.ID, a.Source)
	if err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("replay_traffic: %v", err)}, nil
	}

	var mods httpreplay.Mods
	if a.Modifications != nil {
		m := a.Modifications
		mods = httpreplay.Mods{
			URL:        m.URL,
			Method:     m.Method,
			Headers:    m.Headers,
			Query:      m.Query,
			Body:       m.Body,
			BodyFields: m.BodyFields,
		}
	}

	result, err := httpreplay.Replay(ctx, src, mods)
	if err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("replay_traffic: %v", err)}, nil
	}

	type replayOut struct {
		Method          string            `json:"method"`
		URL             string            `json:"url"`
		StatusCode      int               `json:"status_code"`
		ResponseHeaders map[string]string `json:"response_headers"`
		ResponseBody    string            `json:"response_body"`
	}
	out := replayOut{
		Method:          result.Method,
		URL:             result.URL,
		StatusCode:      result.StatusCode,
		ResponseHeaders: result.ResponseHeaders,
		ResponseBody:    string(result.ResponseBody),
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	return registry.ToolResult{
		Output: string(b),
		Signal: &registry.Signal{
			Kind:     registry.SignalHTTPTrace,
			ToolName: "replay_traffic",
			Content:  fmt.Sprintf("%s %s → %d", result.Method, result.URL, result.StatusCode),
			Detail:   string(b),
		},
	}, nil
}

func (t *replayTrafficTool) resolveSource(ctx context.Context, id int64, preferSource string) (httpreplay.Source, error) {
	// try agent traffic first (has pre-parsed headers)
	if preferSource != "proxy" && t.deps.AgentStore != nil {
		rec, err := t.deps.AgentStore.GetByID(ctx, id)
		if err == nil {
			return httpreplay.Source{
				ID:      rec.ID,
				Method:  rec.Method,
				URL:     rec.URL,
				Headers: rec.RequestHeaders,
				Body:    rec.RequestBody,
			}, nil
		}
	}
	// fall back to proxy traffic — parse raw HTTP to extract headers+body
	if preferSource != "agent" && t.deps.ProxyStore != nil {
		rec, err := t.deps.ProxyStore.GetByID(ctx, id)
		if err == nil {
			src, perr := proxyToSource(rec)
			if perr != nil {
				return httpreplay.Source{}, fmt.Errorf("proxy traffic %d 解析失败: %w", id, perr)
			}
			return src, nil
		}
	}
	return httpreplay.Source{}, fmt.Errorf("未找到 id=%d 的流量记录", id)
}

// proxyToSource 从 ProxyTraffic.RequestRaw 解析出 httpreplay.Source。
// RequestRaw 存储完整 raw HTTP 报文（request-line + headers + body）。
func proxyToSource(rec traffic.ProxyTraffic) (httpreplay.Source, error) {
	if len(rec.RequestRaw) == 0 {
		// RequestRaw 不可用，降级为仅 method+url
		return httpreplay.Source{
			ID:      rec.ID,
			Method:  rec.Method,
			URL:     rec.URL,
			Headers: json.RawMessage("{}"),
		}, nil
	}

	req, err := http.ReadRequest(bufio.NewReader(bytes.NewReader(rec.RequestRaw)))
	if err != nil {
		// 解析失败，降级
		return httpreplay.Source{
			ID:      rec.ID,
			Method:  rec.Method,
			URL:     rec.URL,
			Headers: json.RawMessage("{}"),
		}, nil
	}
	defer req.Body.Close()

	hdrs := make(map[string]string, len(req.Header))
	for k, vs := range req.Header {
		hdrs[strings.ToLower(k)] = strings.Join(vs, ",")
	}
	hdrsJSON, _ := json.Marshal(hdrs)

	var body []byte
	if req.Body != nil {
		buf := new(bytes.Buffer)
		buf.ReadFrom(req.Body)
		body = buf.Bytes()
	}

	return httpreplay.Source{
		ID:      rec.ID,
		Method:  rec.Method,
		URL:     rec.URL,
		Headers: hdrsJSON,
		Body:    body,
	}, nil
}
