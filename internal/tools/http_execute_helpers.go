package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/V3teran/liusha/internal/registry"
	"github.com/V3teran/liusha/internal/traffic"
)

// validateAndNormalizeArgs 验证并规范化参数
func validateAndNormalizeArgs(a *httpRequestArgs) *string {
	if a.URL == "" {
		msg := "http_request: url 必填"
		return &msg
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
	return nil
}

// buildHTTPRequest 构建 HTTP 请求
func (t *httpRequestTool) buildHTTPRequest(ctx context.Context, a *httpRequestArgs) (*http.Request, []appliedCredential, error) {
	// 凭证注入
	applied := t.applyStoredCredentials(ctx, a)

	req, err := http.NewRequestWithContext(ctx, a.Method, a.URL, strings.NewReader(a.Body))
	if err != nil {
		return nil, nil, err
	}

	// 设置 headers
	for k, v := range a.Headers {
		req.Header.Set(k, v)
	}

	// jar 兜底：库读取失败或库中尚无 Cookie 项时，用进程内缓存补会话头
	if req.Header.Get("Cookie") == "" {
		if ch := t.cookieHeader(req.URL.Host); ch != "" {
			req.Header.Set("Cookie", ch)
		}
	}

	// 设置 Content-Type
	if a.Body != "" && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	return req, applied, nil
}

// executeHTTPRequest 执行 HTTP 请求
func executeHTTPRequest(ctx context.Context, req *http.Request, timeoutSeconds int) (*http.Response, int, error) {
	client := &http.Client{
		Timeout: time.Duration(timeoutSeconds) * time.Second,
		// 不自动跳转：跳转会隐藏 30x 语义（认证绕过判定依赖原始响应码）
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	start := time.Now()
	resp, err := client.Do(req)
	durMs := int(time.Since(start).Milliseconds())
	
	return resp, durMs, err
}

// handleResponseCookies 处理响应 Cookie
func (t *httpRequestTool) handleResponseCookies(ctx context.Context, req *http.Request, resp *http.Response) {
	// 响应 Set-Cookie 入 jar（缓存）+ 变更时合并写入凭证库
	t.storeCookies(req.URL.Host, resp.Cookies())
	t.syncSessionToStore(ctx, req.URL.Host)
}

// recordTraffic 记录流量到弹药库
func (t *httpRequestTool) recordTraffic(ctx context.Context, req *http.Request, resp *http.Response, a *httpRequestArgs, respBody []byte, durMs int) int64 {
	if t.deps.AgentStore == nil {
		return 0
	}

	reqHeaders, _ := json.Marshal(flattenHeaders(req.Header))
	respHeaders, _ := json.Marshal(flattenHeaders(resp.Header))
	
	trafficID, appendErr := t.deps.AgentStore.Append(ctx, traffic.AgentTraffic{
		TaskID:          t.deps.TaskID,
		AgentRunID:      t.deps.AgentRunID,
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
		return 0
	}
	
	return trafficID
}

// buildToolResult 构建工具返回结果
func (t *httpRequestTool) buildToolResult(req *http.Request, resp *http.Response, a *httpRequestArgs, respBody []byte, durMs int, trafficID int64, applied []appliedCredential) registry.ToolResult {
	snippet := respBody
	if len(snippet) > httpRespBodySnippet {
		snippet = snippet[:httpRespBodySnippet]
	}

	// 返回完整的 request/response 信息，供 write_observation 构造自包含的 repro
	out, _ := json.Marshal(map[string]interface{}{
		"traffic_id": trafficID,
		"session": map[string]interface{}{
			"cookies":             t.sessionCookieNames(req.URL.Host),
			"applied_credentials": applied,
		},
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
	
	return registry.ToolResult{Output: string(out)}
}
