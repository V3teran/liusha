package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/V3teran/liusha/internal/registry"
	"github.com/V3teran/liusha/internal/traffic"
)

// ─── http_request ────────────────────────────────────────────────────────────
//
// 带抓流的 typed HTTP 工具：主进程侧发请求 + 落 agent_traffic（返回 traffic_id）。
//
// 架构位置：它是 browser-svc.py（CDP capture）的对位物——容器内 mitm 抓流层缺失时
// （Dockerfile 未装 mitmproxy），CLI 工具的 HTTP 无法进入复现弹药库。本工具把
// executor 的 HTTP 操作收拢到 typed 通道：请求自动入 agent_traffic → LLM 拿到
// traffic_id → write_observation(repro={traffic_id, assert}) → evaluator 重放坐实。
// 晋升链的完整闭环依赖此工具提供的「可复现原语」。

var httpRequestSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "url":            {"type": "string", "description": "完整 URL（含 http/https）"},
    "method":         {"type": "string", "enum": ["GET","POST","PUT","DELETE","PATCH","HEAD","OPTIONS"], "description": "HTTP 方法，默认 GET"},
    "headers":        {"type": "object", "description": "请求头 key→value（可选）"},
    "body":           {"type": "string", "description": "请求体（可选）"},
    "timeout_seconds": {"type": "integer", "description": "超时秒数，默认 30，上限 120"}
  },
  "required": ["url"]
}`)

const (
	httpReqMaxTimeout   = 120
	httpRespBodySnippet = 4096 // 回给 LLM 的响应体截断
)

type httpRequestTool struct {
	registry.BaseTool
	deps Deps
}

func newHTTPRequestTool(deps Deps, timeout time.Duration, safe bool) *httpRequestTool {
	t := &httpRequestTool{deps: deps}
	t.SetTimeout(timeout)
	t.SetConcurrencySafe(safe)
	return t
}

func (t *httpRequestTool) Name() string { return "http_request" }
func (t *httpRequestTool) ShortDesc() string {
	return "发 HTTP 请求并返回完整 request/response"
}
func (t *httpRequestTool) Desc() string {
	return "发送 HTTP 请求并返回完整的 request（method/url/headers/body）与 response——" +
		"write_observation 据此构造自包含复现配方（repro.request 完整拷贝本请求的 request 并注入 payload）。"
}
func (t *httpRequestTool) Schema() json.RawMessage { return httpRequestSchema }

func (t *httpRequestTool) Execute(ctx context.Context, args json.RawMessage) (registry.ToolResult, error) {
	var a struct {
		URL            string            `json:"url"`
		Method         string            `json:"method"`
		Headers        map[string]string `json:"headers"`
		Body           string            `json:"body"`
		TimeoutSeconds int               `json:"timeout_seconds"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return registry.ToolResult{Error: "http_request: 解析参数失败: " + err.Error()}, nil
	}
	if a.URL == "" {
		return registry.ToolResult{Error: "http_request: url 必填"}, nil
	}
	if a.Method == "" {
		a.Method = http.MethodGet
	}
	if a.TimeoutSeconds <= 0 {
		a.TimeoutSeconds = 30
	}
	if a.TimeoutSeconds > httpReqMaxTimeout {
		a.TimeoutSeconds = httpReqMaxTimeout
	}

	req, err := http.NewRequestWithContext(ctx, a.Method, a.URL, strings.NewReader(a.Body))
	if err != nil {
		return registry.ToolResult{Error: "http_request: 构造请求失败: " + err.Error()}, nil
	}
	for k, v := range a.Headers {
		req.Header.Set(k, v)
	}
	if a.Body != "" && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	client := &http.Client{
		Timeout: time.Duration(a.TimeoutSeconds) * time.Second,
		// 不自动跳转：跳转会隐藏 30x 语义（认证绕过判定依赖原始响应码）。
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	start := time.Now()
	resp, err := client.Do(req)
	durMs := int(time.Since(start).Milliseconds())
	if err != nil {
		return registry.ToolResult{Error: "http_request: 请求失败: " + err.Error()}, nil
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 上限 1MiB

	// 抓流入弹药库（best-effort：落库失败不阻塞请求本身，但 LLM 拿不到 traffic_id）
	trafficID := int64(0)
	if t.deps.AgentStore != nil {
		reqHeaders, _ := json.Marshal(flattenHeaders(req.Header))
		respHeaders, _ := json.Marshal(flattenHeaders(resp.Header))
		id, appendErr := t.deps.AgentStore.Append(ctx, traffic.AgentTraffic{
			TaskID:          t.deps.TaskID,
			AgentID:         t.deps.AgentID,
			Tool:            "http_request",
			Method:          a.Method,
			URL:             a.URL,
			RequestHeaders:  reqHeaders,
			RequestBody:     []byte(a.Body),
			StatusCode:      resp.StatusCode,
			ResponseHeaders: respHeaders,
			ResponseBody:    respBody,
			DurationMs:      durMs,
		})
		if appendErr != nil {
			// 记录失败只影响复现能力，不影响本次观测
			_ = appendErr
		}
		trafficID = id
	}

	snippet := respBody
	if len(snippet) > httpRespBodySnippet {
		snippet = snippet[:httpRespBodySnippet]
	}

	// 返回完整的 request/response 信息，供 write_observation 构造自包含的 repro。
	// request.headers 回显实际发送的最终 headers（含自动补的 Content-Type 等）——
	// LLM 拷贝此对象构造 repro.request 才能忠实重放（输入 headers 可能缺 CT 导致假证伪）。
	out, _ := json.Marshal(map[string]interface{}{
		"traffic_id": trafficID,
		"request": map[string]interface{}{
			"method":  a.Method,
			"url":     a.URL,
			"headers": flattenHeaders(req.Header),
			"body":    a.Body,
		},
		"response": map[string]interface{}{
			"status_code":  resp.StatusCode,
			"headers":      flattenHeaders(resp.Header),
			"body":         string(snippet),
			"truncated":    len(respBody) > len(snippet),
			"duration_ms":  durMs,
			"content_type": resp.Header.Get("Content-Type"),
		},
	})
	return registry.ToolResult{Output: string(out)}, nil
}

// flattenHeaders 把 http.Header 摊平成单值 map（多值取第一个）——agent_traffic 的
// request/response_headers 列按单值 JSON 对象存（与 browser/CLI 抓流口径一致）。
func flattenHeaders(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k, vs := range h {
		if len(vs) > 0 {
			out[strings.ToLower(k)] = vs[0]
		}
	}
	return out
}
