package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"strconv"

	"github.com/V3teran/liusha/internal/registry"
	"github.com/V3teran/liusha/internal/traffic"
)

// ─── 流量记录的自描述 id ─────────────────────────────────────────────────────
//
// agent_traffic 与 proxy_traffic 是两张独立自增表，裸数字 id 跨表撞号（实测两表
// max 同号共存）。工具边界上的流量 id 一律为「来源前缀化」的字符串——
// "proxy:126" / "agent:94"：traffic_list 返回它，traffic_view 解析它并**严格路由**
// 到对应表，不做任何跨表回退猜测。正确性由数据形状保证，不依赖调用方（LLM）
// 记得传 source。
const (
	trafficSourceProxy = "proxy"
	trafficSourceAgent = "agent"
)

// qualifyTrafficRef 构造带来源前缀的流量引用 id。
func qualifyTrafficRef(source string, id int64) string {
	return source + ":" + strconv.FormatInt(id, 10)
}

// parseTrafficRef 解析带来源前缀的流量引用 id。裸数字（无前缀）一律拒绝——
// 跨表撞号下裸 id 语义不明，宁报错不可错配。
func parseTrafficRef(ref string) (source string, id int64, err error) {
	src, num, ok := strings.Cut(strings.TrimSpace(ref), ":")
	if !ok || (src != trafficSourceProxy && src != trafficSourceAgent) {
		return "", 0, fmt.Errorf("流量 id 须为 %q 或 %q 形式（traffic_list 返回的 id），得到 %q", "proxy:<id>", "agent:<id>", ref)
	}
	id, e := strconv.ParseInt(num, 10, 64)
	if e != nil || id <= 0 {
		return "", 0, fmt.Errorf("流量 id 数字非法: %q", ref)
	}
	return src, id, nil
}

// ─── traffic_list ────────────────────────────────────────────────────────────

var listTrafficSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "source":      {"type": "string", "enum": ["agent", "proxy"], "description": "流量来源过滤（可选）：agent=sandbox 内工具自产流量，proxy=代理捕获抓包流量。"},
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

func newListTrafficTool(deps Deps, timeout time.Duration) *listTrafficTool {
	t := &listTrafficTool{deps: deps}
	t.SetTimeout(timeout)
	return t
}

func (t *listTrafficTool) Name() string      { return "traffic_list" }
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
		return registry.ToolResult{Error: fmt.Sprintf("traffic_list: invalid args: %v", err)}, nil
	}
	if a.Limit <= 0 {
		a.Limit = 20
	}

	type row struct {
		ID         string    `json:"id"` // 自描述引用 id："proxy:126" / "agent:94"（跨表撞号免疫）
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
	var degraded []string

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
		if err != nil {
			// 查询失败静默吞掉会让 agent 以为「没有弹药」——必须留痕并给降级说明
			t.deps.Logger.Warn().Err(err).Str("task_id", t.deps.TaskID).Msg("traffic_list: agent 流量查询失败")
			degraded = append(degraded, "agent 流量查询失败（该来源证据暂缺）")
		} else {
			for _, s := range list {
				rows = append(rows, row{
					ID: qualifyTrafficRef(trafficSourceAgent, s.ID), Source: trafficSourceAgent,
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
		if err != nil {
			t.deps.Logger.Warn().Err(err).Str("task_id", t.deps.TaskID).Msg("traffic_list: proxy 流量查询失败")
			degraded = append(degraded, "proxy 流量查询失败（该来源证据暂缺）")
		} else {
			for _, s := range list {
				rows = append(rows, row{
					ID: qualifyTrafficRef(trafficSourceProxy, s.ID), Source: trafficSourceProxy,
					Method: s.Method, Path: s.Path, URL: s.URL,
					StatusCode: s.StatusCode,
					CreatedAt:  s.CapturedAt,
				})
			}
		}
	}

	if len(rows) == 0 {
		if len(degraded) > 0 {
			return registry.ToolResult{Output: "[]", Error: strings.Join(degraded, "；")}, nil
		}
		return registry.ToolResult{Output: "[]"}, nil
	}
	b, _ := json.MarshalIndent(rows, "", "  ")
	return registry.ToolResult{Output: string(b)}, nil
}

// ─── traffic_view ────────────────────────────────────────────────────────────

var viewTrafficSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "id": {"type": "string", "description": "流量记录引用 id（traffic_list 返回的 \"proxy:123\" / \"agent:94\" 形式，自带来源前缀）。"}
  },
  "required": ["id"]
}`)

type viewTrafficTool struct {
	registry.BaseTool
	deps Deps
}

func newViewTrafficTool(deps Deps, timeout time.Duration) *viewTrafficTool {
	t := &viewTrafficTool{deps: deps}
	t.SetTimeout(timeout)
	return t
}

func (t *viewTrafficTool) Name() string      { return "traffic_view" }
func (t *viewTrafficTool) ShortDesc() string { return "查看单条历史流量完整内容" }
func (t *viewTrafficTool) Desc() string {
	return "取单条历史流量的完整 raw HTTP 请求+响应（headers/body 全量）。"
}
func (t *viewTrafficTool) Schema() json.RawMessage { return viewTrafficSchema }

func (t *viewTrafficTool) Execute(ctx context.Context, args json.RawMessage) (registry.ToolResult, error) {
	var a struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return registry.ToolResult{Error: "traffic_view: 解析参数失败: " + err.Error()}, nil
	}

	// id 自带来源前缀（traffic_list 返回 "proxy:126" / "agent:94"），据此严格路由到
	// 对应表——不做跨表回退猜测（两表 id 独立自增会撞号，回退顺序是错配的根源）。
	source, id, perr := parseTrafficRef(a.ID)
	if perr != nil {
		return registry.ToolResult{Error: "traffic_view: " + perr.Error()}, nil
	}

	switch source {
	case trafficSourceAgent:
		if t.deps.AgentStore == nil {
			return registry.ToolResult{Error: "traffic_view: agent 流量存储未装配"}, nil
		}
		rec, err := t.deps.AgentStore.GetByID(ctx, id)
		if err != nil {
			return registry.ToolResult{Error: fmt.Sprintf("traffic_view: agent 流量 %s 不存在", a.ID)}, nil
		}
		b, _ := json.MarshalIndent(agentTrafficView(rec), "", "  ")
		return registry.ToolResult{
			Output: string(b),
			Signal: &registry.Signal{Kind: registry.SignalHTTPTrace, ToolName: "traffic_view",
				Content: fmt.Sprintf("[agent] %s %s → %d", rec.Method, rec.URL, rec.StatusCode)},
		}, nil

	default: // trafficSourceProxy
		if t.deps.ProxyStore == nil {
			return registry.ToolResult{Error: "traffic_view: proxy 流量存储未装配"}, nil
		}
		rec, err := t.deps.ProxyStore.GetByID(ctx, id)
		if err != nil {
			return registry.ToolResult{Error: fmt.Sprintf("traffic_view: proxy 流量 %s 不存在", a.ID)}, nil
		}
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
			Signal: &registry.Signal{Kind: registry.SignalHTTPTrace, ToolName: "traffic_view",
				Content: fmt.Sprintf("[proxy] %s %s → %d", rec.Method, rec.URL, rec.StatusCode)},
		}, nil
	}
}

// agentTrafficViewRecord 是 traffic_view 给出的一条 agent 流量：请求与响应体是文本。
type agentTrafficViewRecord struct {
	ID              string          `json:"id"`
	Identity        string          `json:"identity,omitempty"`
	Tool            string          `json:"tool,omitempty"`
	Method          string          `json:"method"`
	URL             string          `json:"url"`
	StatusCode      int             `json:"status_code"`
	RequestHeaders  json.RawMessage `json:"request_headers,omitempty"`
	RequestBody     string          `json:"request_body"`
	ResponseHeaders json.RawMessage `json:"response_headers,omitempty"`
	ResponseBody    string          `json:"response_body"`
	DurationMs      int             `json:"duration_ms"`
	CreatedAt       time.Time       `json:"created_at"`
}

// agentTrafficView 把记录转成给读者看的视图。体直接序列化 []byte 会变成 base64，按明文核对
// 出处时会误判「记录里没有」；非 UTF-8 的二进制体只注明字节数。
func agentTrafficView(rec traffic.AgentTraffic) agentTrafficViewRecord {
	return agentTrafficViewRecord{
		ID: qualifyTrafficRef(trafficSourceAgent, rec.ID), Identity: rec.Identity, Tool: rec.Tool,
		Method: rec.Method, URL: rec.URL, StatusCode: rec.StatusCode,
		RequestHeaders: rec.RequestHeaders, RequestBody: bodyText(rec.RequestBody),
		ResponseHeaders: rec.ResponseHeaders, ResponseBody: bodyText(rec.ResponseBody),
		DurationMs: rec.DurationMs, CreatedAt: rec.CreatedAt,
	}
}

func bodyText(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	return fmt.Sprintf("（二进制内容，%d 字节）", len(b))
}
