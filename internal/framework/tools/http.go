package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ─────────────────────────────────────────────
//  HTTP GET 工具
// ─────────────────────────────────────────────

// HTTPGetTool HTTP GET 请求工具
type HTTPGetTool struct {
	client  *http.Client
	timeout time.Duration
}

// NewHTTPGetTool 创建 HTTP GET 工具
func NewHTTPGetTool(timeout time.Duration) *HTTPGetTool {
	return &HTTPGetTool{
		client: &http.Client{
			Timeout: timeout,
		},
		timeout: timeout,
	}
}

// Name 工具名称
func (t *HTTPGetTool) Name() string {
	return "http_get"
}

// Description 工具描述
func (t *HTTPGetTool) Description() string {
	return "发送 HTTP GET 请求获取网页内容"
}

// Parameters 参数 Schema
func (t *HTTPGetTool) Parameters() json.RawMessage {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url": map[string]any{
				"type":        "string",
				"description": "目标 URL",
			},
			"headers": map[string]any{
				"type":        "object",
				"description": "请求头（可选）",
			},
		},
		"required": []string{"url"},
	}
	data, _ := json.Marshal(schema)
	return data
}

// Execute 执行工具
func (t *HTTPGetTool) Execute(ctx context.Context, input string) (string, error) {
	// 解析输入
	var params struct {
		URL     string            `json:"url"`
		Headers map[string]string `json:"headers"`
	}

	if err := json.Unmarshal([]byte(input), &params); err != nil {
		return "", fmt.Errorf("参数解析失败: %w", err)
	}

	if params.URL == "" {
		return "", fmt.Errorf("URL 不能为空")
	}

	// 创建请求
	req, err := http.NewRequestWithContext(ctx, "GET", params.URL, nil)
	if err != nil {
		return "", fmt.Errorf("创建请求失败: %w", err)
	}

	// 设置请求头
	for key, value := range params.Headers {
		req.Header.Set(key, value)
	}

	// 发送请求
	resp, err := t.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	// 读取响应
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("读取响应失败: %w", err)
	}

	// 检查状态码
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("HTTP 错误: %d %s", resp.StatusCode, string(body))
	}

	return string(body), nil
}

// ─────────────────────────────────────────────
//  HTTP POST 工具
// ─────────────────────────────────────────────

// HTTPPostTool HTTP POST 请求工具
type HTTPPostTool struct {
	client  *http.Client
	timeout time.Duration
}

// NewHTTPPostTool 创建 HTTP POST 工具
func NewHTTPPostTool(timeout time.Duration) *HTTPPostTool {
	return &HTTPPostTool{
		client: &http.Client{
			Timeout: timeout,
		},
		timeout: timeout,
	}
}

// Name 工具名称
func (t *HTTPPostTool) Name() string {
	return "http_post"
}

// Description 工具描述
func (t *HTTPPostTool) Description() string {
	return "发送 HTTP POST 请求"
}

// Parameters 参数 Schema
func (t *HTTPPostTool) Parameters() json.RawMessage {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url": map[string]any{
				"type":        "string",
				"description": "目标 URL",
			},
			"body": map[string]any{
				"type":        "string",
				"description": "请求体",
			},
			"content_type": map[string]any{
				"type":        "string",
				"description": "Content-Type（默认 application/json）",
			},
			"headers": map[string]any{
				"type":        "object",
				"description": "请求头（可选）",
			},
		},
		"required": []string{"url", "body"},
	}
	data, _ := json.Marshal(schema)
	return data
}

// Execute 执行工具
func (t *HTTPPostTool) Execute(ctx context.Context, input string) (string, error) {
	// 解析输入
	var params struct {
		URL         string            `json:"url"`
		Body        string            `json:"body"`
		ContentType string            `json:"content_type"`
		Headers     map[string]string `json:"headers"`
	}

	if err := json.Unmarshal([]byte(input), &params); err != nil {
		return "", fmt.Errorf("参数解析失败: %w", err)
	}

	if params.URL == "" {
		return "", fmt.Errorf("URL 不能为空")
	}

	// 默认 Content-Type
	if params.ContentType == "" {
		params.ContentType = "application/json"
	}

	// 创建请求
	req, err := http.NewRequestWithContext(ctx, "POST", params.URL, strings.NewReader(params.Body))
	if err != nil {
		return "", fmt.Errorf("创建请求失败: %w", err)
	}

	// 设置 Content-Type
	req.Header.Set("Content-Type", params.ContentType)

	// 设置其他请求头
	for key, value := range params.Headers {
		req.Header.Set(key, value)
	}

	// 发送请求
	resp, err := t.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	// 读取响应
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("读取响应失败: %w", err)
	}

	// 检查状态码
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("HTTP 错误: %d %s", resp.StatusCode, string(body))
	}

	return string(body), nil
}
