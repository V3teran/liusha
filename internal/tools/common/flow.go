// Package common 中的 flow.go：list_flows / view_flow / replay_flow 三个工具实现。
//
// 这三个工具是 0060+ "统一流量字典" 的 LLM 入口：
//
//	list_flows   按 owner+filter 列出历史 HTTP 流量摘要
//	view_flow    单条 raw HTTP 请求 + 响应详情
//	replay_flow  按 id 复用历史请求，可 modifications 改部分字段后重发
//	             重发流量自动经 liusha proxy 8890（HTTP_PROXY + Proxy-Authorization）
//	             → internal 字典 → 后续 list_flows 可看见
//
// 设计要点：
//   - 工具的 Store 字段是窄接口（FlowStore）便于单元测试
//   - OwnerID 注入式，LLM 不可控（防串库）
//   - ReplayFlow 用 net/http.Client + Transport.Proxy 经 127.0.0.1:8890，TLS InsecureSkipVerify
//     （目标可能是自签 / 测试站，且流量进自家字典无中间人风险）
package common

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/V3teran/liusha/internal/flow"
	toolfx "github.com/V3teran/liusha/internal/toolruntime"
)

// FlowStore 是 3 个 flow 工具依赖的最小接口。
type FlowStore interface {
	ListByOwnerFiltered(ctx context.Context, ownerID string, f flow.ListFilter) ([]flow.FlowSummary, error)
	GetByID(ctx context.Context, id int64) (flow.Flow, error)
}

// ─── list_flows ──────────────────────────────────────────────

// ListFlows 列出 owner 范围内的历史 HTTP 流量摘要（不含 body / headers 详情）。
type ListFlows struct {
	Store     FlowStore
	OwnerType string // 装配时注入（passive_session / active_scan）
	OwnerID   string // 装配时注入，防 LLM 串库
	Host      string // 当前 hunter 的 host，默认 filter
}

// Name 返回工具名 "list_flows"。
func (a *ListFlows) Name() string { return "list_flows" }

// Description 给 LLM 看的简短说明。
func (a *ListFlows) Description() string {
	return "列出 owner 范围内的历史 HTTP 流量摘要。默认按当前 host 过滤，可叠加 method/path/source/status/since。" +
		"返回 [{id, method, host, path, status, duration_ms, created_at}]；要看完整请求体调 view_flow。"
}

// ParametersJSON 返工具参数 JSON schema。
func (a *ListFlows) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{
      "type":"object",
      "properties": {
        "host":   {"type":"string", "description":"host filter，留空则用当前 hunter host"},
        "method": {"type":"string", "description":"HTTP method 过滤（自动大写）"},
        "path":   {"type":"string", "description":"path glob 过滤，支持 '*'（如 '/admin/*' / '/api/users/*'）"},
        "source": {"type":"string", "enum":["external","internal"], "description":"流量来源；external=用户/Burp 抓的，internal=agent 工具发的"},
        "status_min": {"type":"integer", "description":"响应状态码下界（如 400 → 仅 4xx/5xx）"},
        "status_max": {"type":"integer", "description":"响应状态码上界"},
        "since": {"type":"string", "description":"ISO 时间戳，仅看此后流量（如 '2026-05-26T00:00:00Z'）"},
        "limit": {"type":"integer", "default":50, "maximum":200},
        "offset": {"type":"integer", "default":0}
      }
    }`)
}

// Execute 调 Store.ListByOwnerFiltered。
func (a *ListFlows) Execute(ctx context.Context, args json.RawMessage) (toolfx.Result, error) {
	var in struct {
		Host      string `json:"host"`
		Method    string `json:"method"`
		Path      string `json:"path"`
		Source    string `json:"source"`
		StatusMin int    `json:"status_min"`
		StatusMax int    `json:"status_max"`
		Since     string `json:"since"`
		Limit     int    `json:"limit"`
		Offset    int    `json:"offset"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &in); err != nil {
			return toolfx.Result{}, fmt.Errorf("解析 list_flows 参数失败: %w", err)
		}
	}
	// host 默认取当前 hunter host（防 LLM 漏传查到无关 host）
	host := strings.TrimSpace(in.Host)
	if host == "" {
		host = a.Host
	}
	method := strings.ToUpper(strings.TrimSpace(in.Method))
	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	filter := flow.ListFilter{
		Host:      host,
		Method:    method,
		Path:      strings.TrimSpace(in.Path),
		Source:    strings.TrimSpace(in.Source),
		StatusMin: in.StatusMin,
		StatusMax: in.StatusMax,
		Limit:     limit,
		Offset:    in.Offset,
	}
	if in.Since != "" {
		if t, perr := time.Parse(time.RFC3339, in.Since); perr == nil {
			filter.Since = t
		}
	}

	rows, err := a.Store.ListByOwnerFiltered(ctx, a.OwnerID, filter)
	if err != nil {
		return toolfx.Result{}, fmt.Errorf("查询 flow 列表失败: %w", err)
	}

	type rowOut struct {
		ID         int64     `json:"id"`
		Method     string    `json:"method"`
		Host       string    `json:"host"`
		Path       string    `json:"path"`
		Status     int       `json:"status"`
		Source     string    `json:"source"`
		DurationMs int       `json:"duration_ms"`
		CreatedAt  time.Time `json:"created_at"`
	}
	out := make([]rowOut, 0, len(rows))
	for _, r := range rows {
		out = append(out, rowOut{
			ID: r.ID, Method: r.Method, Host: r.Host, Path: r.Path,
			Status: r.StatusCode, Source: r.Source, DurationMs: r.DurationMs, CreatedAt: r.CreatedAt,
		})
	}
	payload, _ := json.Marshal(map[string]any{"count": len(out), "flows": out})
	return toolfx.Result{Output: payload}, nil
}

// ─── view_flow ───────────────────────────────────────────────

// ViewFlow 拿一条流量的完整 raw HTTP 请求 + 响应（headers / body 全有）。
type ViewFlow struct {
	Store     FlowStore
	OwnerType string
	OwnerID   string
}

// Name 返回工具名 "view_flow"。
func (a *ViewFlow) Name() string { return "view_flow" }

// Description 给 LLM 看的简短说明。
func (a *ViewFlow) Description() string {
	return "拿单条历史流量的完整 raw HTTP 请求 + 响应（headers / body 全有）。" +
		"看 cookie / auth header / token / 完整 payload / 完整响应。**id 来自 list_flows**。"
}

// ParametersJSON schema。
func (a *ViewFlow) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{
      "type":"object",
      "properties": {"id":{"type":"integer","description":"flow id（来自 list_flows 返回）"}},
      "required":["id"]
    }`)
}

// Execute 调 Store.GetByID + 校验 owner 隔离。
func (a *ViewFlow) Execute(ctx context.Context, args json.RawMessage) (toolfx.Result, error) {
	var in struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return toolfx.Result{}, fmt.Errorf("解析 view_flow 参数失败: %w", err)
	}
	if in.ID <= 0 {
		return toolfx.Result{}, fmt.Errorf("id 必填且 > 0")
	}
	f, err := a.Store.GetByID(ctx, in.ID)
	if err != nil {
		return toolfx.Result{}, fmt.Errorf("flow %d 不存在或读失败: %w", in.ID, err)
	}
	// 防越权：owner 不匹配直接拒
	if f.OwnerID != a.OwnerID {
		return toolfx.Result{}, fmt.Errorf("flow %d 不属于当前 owner（拒绝跨 owner 访问）", in.ID)
	}

	out := map[string]any{
		"id":               f.ID,
		"source":           f.Source,
		"host":             f.Host,
		"method":           f.Method,
		"url":              f.URL,
		"path":             f.Path,
		"request_headers":  json.RawMessage(f.RequestHeaders),
		"request_body":     string(f.RequestBody),
		"status_code":      f.StatusCode,
		"response_headers": json.RawMessage(f.ResponseHeaders),
		"response_body":    string(f.ResponseBody),
		"duration_ms":      f.DurationMs,
		"created_at":       f.CreatedAt,
	}
	payload, _ := json.Marshal(out)
	return toolfx.Result{Output: payload}, nil
}

// ─── replay_flow ─────────────────────────────────────────────

// ReplayFlow 按 id 复用历史请求重发；可 modifications 改部分字段。
// 未指定的字段从原请求继承（含 cookie / CSRF token / auth header / 其它 form 字段）。
//
// 重发流量经 liusha proxy（127.0.0.1:8890 + Proxy-Auth basic auth = hunter_<HunterID>:_）
// → 自动进 internal 字典，下次 list_flows 可看见新一行。
type ReplayFlow struct {
	Store     FlowStore
	OwnerType string
	OwnerID   string
	HunterID  string // 当前 hunter id，用于 Proxy-Auth basic auth
	ProxyAddr string // liusha proxy agent listener "host:port"，如 "127.0.0.1:8890"；空则跳过 proxy
}

// Name 返回工具名 "replay_flow"。
func (a *ReplayFlow) Name() string { return "replay_flow" }

// Description 给 LLM 看的简短说明。
func (a *ReplayFlow) Description() string {
	return "重发历史 HTTP 流量；modifications 改部分字段，未指定的全继承原请求（cookie/CSRF/auth/form 字段）。" +
		"比手写 curl 准 100 倍——session 上下文自动保留。返回新 flow id + 响应详情。"
}

// ParametersJSON schema。
func (a *ReplayFlow) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{
      "type":"object",
      "properties": {
        "id": {"type":"integer", "description":"原 flow id（来自 list_flows / view_flow）"},
        "modifications": {
          "type":"object",
          "description":"可选修改；未指定字段全继承原请求",
          "properties": {
            "url":    {"type":"string", "description":"换完整 URL（含 path + query）"},
            "method": {"type":"string", "description":"换 HTTP method"},
            "headers": {"type":"object", "description":"header dict 增删改；value=null 删该 header"},
            "body":   {"type":"string", "description":"替换整个 body"}
          }
        }
      },
      "required":["id"]
    }`)
}

// Execute 拿原 flow + apply modifications + 经 liusha proxy 重发。
func (a *ReplayFlow) Execute(ctx context.Context, args json.RawMessage) (toolfx.Result, error) {
	var in struct {
		ID            int64 `json:"id"`
		Modifications struct {
			URL     string             `json:"url"`
			Method  string             `json:"method"`
			Headers map[string]*string `json:"headers"` // nil = 删
			Body    *string            `json:"body"`
		} `json:"modifications"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return toolfx.Result{}, fmt.Errorf("解析 replay_flow 参数失败: %w", err)
	}
	if in.ID <= 0 {
		return toolfx.Result{}, fmt.Errorf("id 必填且 > 0")
	}

	f, err := a.Store.GetByID(ctx, in.ID)
	if err != nil {
		return toolfx.Result{}, fmt.Errorf("flow %d 不存在或读失败: %w", in.ID, err)
	}
	if f.OwnerID != a.OwnerID {
		return toolfx.Result{}, fmt.Errorf("flow %d 不属于当前 owner（拒绝跨 owner replay）", in.ID)
	}

	// 拼新请求：method/url/headers/body 从原 flow 起步，应用 modifications
	method := f.Method
	if in.Modifications.Method != "" {
		method = strings.ToUpper(in.Modifications.Method)
	}
	rawURL := f.URL
	if in.Modifications.URL != "" {
		rawURL = in.Modifications.URL
	}
	body := f.RequestBody
	if in.Modifications.Body != nil {
		body = []byte(*in.Modifications.Body)
	}

	// 反序列化原 request_headers jsonb → map[string]string，apply mod 增删改
	headers := map[string]string{}
	if len(f.RequestHeaders) > 0 {
		_ = json.Unmarshal(f.RequestHeaders, &headers)
	}
	for k, v := range in.Modifications.Headers {
		key := strings.ToLower(k)
		if v == nil {
			delete(headers, key)
		} else {
			headers[key] = *v
		}
	}

	// 构造 http.Request
	req, err := http.NewRequestWithContext(ctx, method, rawURL, bytes.NewReader(body))
	if err != nil {
		return toolfx.Result{}, fmt.Errorf("构造请求失败: %w", err)
	}
	for k, v := range headers {
		// canonical key 避免 LLM 传 "content-type" vs "Content-Type" 分歧
		req.Header.Set(http.CanonicalHeaderKey(k), v)
	}

	// 配置 transport：走 liusha proxy + Proxy-Auth + TLS InsecureSkipVerify
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // self-issued MITM cert
	}
	if a.ProxyAddr != "" && a.HunterID != "" {
		proxyURL, perr := url.Parse(fmt.Sprintf("http://hunter_%s:_@%s", a.HunterID, a.ProxyAddr))
		if perr == nil {
			transport.Proxy = http.ProxyURL(proxyURL)
		}
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		return toolfx.Result{}, fmt.Errorf("replay 请求失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024)) // 截断 256 KiB 给 LLM
	respHeaders := map[string]string{}
	for k, v := range resp.Header {
		if len(v) > 0 {
			respHeaders[strings.ToLower(k)] = strings.Join(v, ",")
		}
	}

	out := map[string]any{
		"original_flow_id": f.ID,
		"replayed": map[string]any{
			"method":           method,
			"url":              rawURL,
			"request_headers":  headers,
			"request_body_len": len(body),
		},
		"response": map[string]any{
			"status_code":       resp.StatusCode,
			"response_headers":  respHeaders,
			"response_body":     string(respBody),
			"response_body_len": len(respBody),
		},
		"note": "新流量已经 liusha proxy 入字典（source=internal），下次 list_flows 可见",
	}
	payload, _ := json.Marshal(out)
	return toolfx.Result{Output: payload}, nil
}
