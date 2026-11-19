package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// TSecBench 统一错误码（见 guide_1.md §4.2）。
const (
	codeTaskNotFound        = "task_not_found"       // 404：token 失效/缺失 → 停
	codeChallengeNotFound   = "challenge_not_found"  // 404：unique_code 不在用例集 → 跳
	codeInvalidState        = "invalid_state"        // 409：任务结束 | 活跃满 | 通关后看 hint
	codeDuplicate           = "duplicate"            // 409：flag 已正确提交 → 跳
	codeResourceUnavailable = "resource_unavailable" // 503：靶场资源未就绪 → 重试
	codeInternalError       = "internal_error"       // 500：未预期错误 → 重试一次
)

// benchError 是 TSecBench 业务异常（统一错误体），携带 HTTP 状态码便于分类处置。
type benchError struct {
	HTTPStatus int             `json:"-"`
	Code       string          `json:"code"`
	Message    string          `json:"message"`
	Detail     json.RawMessage `json:"detail"`
}

func (e *benchError) Error() string {
	return fmt.Sprintf("tsecbench %d %s: %s", e.HTTPStatus, e.Code, e.Message)
}

// tokenInvalid 报告是否为 token 失效（task_not_found）——终止条件，停并报告用户。
func (e *benchError) tokenInvalid() bool { return e.Code == codeTaskNotFound }

// isMaxActive 报告 invalid_state 是否因活跃题达上限（可先 close 再 start）。
// 平台 message 自由文本，按关键词综合判定（英文 "max active" / 中文"上限"）。
func (e *benchError) isMaxActive() bool {
	if e.Code != codeInvalidState {
		return false
	}
	m := strings.ToLower(e.Message)
	return strings.Contains(m, "max") || strings.Contains(m, "active") ||
		strings.Contains(e.Message, "上限") || strings.Contains(e.Message, "达到")
}

// taskEnded 报告 invalid_state 是否因整个跑分任务已结束（超时/停止）——终止条件。
// 与「活跃满」互斥：活跃满可恢复，任务结束不可恢复。
func (e *benchError) taskEnded() bool {
	return e.Code == codeInvalidState && !e.isMaxActive()
}

// retriable 报告是否可短暂重试（靶场资源未就绪 / 系统内部错误）。
func (e *benchError) retriable() bool {
	return e.Code == codeResourceUnavailable || e.Code == codeInternalError
}

// noRedirect 让客户端不跟随重定向：令牌在自定义头 BENCHMARK_TOKEN 里，标准库跟随重定向时
// 只剥离 Authorization、Cookie 等标准凭据头，自定义头会被带到跳转目标。答题 API 不应重定向，
// 收到 3xx 按非 2xx 响应报错。
func noRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// benchClient 是 TSecBench 答题 API 客户端。所有 /openapi/v1/challenges/* 请求携带
// BENCHMARK_TOKEN 头。
type benchClient struct {
	base   string
	token  string
	client *http.Client
}

// newBenchClient 构造客户端；client 为 nil 时用带 60s 超时、不跟随重定向的客户端。
func newBenchClient(base, token string, client *http.Client) *benchClient {
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second, CheckRedirect: noRedirect}
	}
	return &benchClient{base: strings.TrimRight(base, "/"), token: token, client: client}
}

// do 发起带 token 的请求并解码成功响应；非 2xx 解析为 benchError（业务错误体）或裸状态错误。
func (c *benchClient) do(ctx context.Context, method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("编码请求体: %w", err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rdr)
	if err != nil {
		return fmt.Errorf("构造请求 %s %s: %w", method, path, err)
	}
	req.Header.Set("BENCHMARK_TOKEN", c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		be := &benchError{HTTPStatus: resp.StatusCode}
		if json.Unmarshal(raw, be) == nil && be.Code != "" {
			return be
		}
		// 非业务错误体（如 422 框架校验、网关 HTML）：裸状态错误。
		return fmt.Errorf("%s %s: HTTP %d: %s", method, path, resp.StatusCode, truncate(string(raw), 256))
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("解析 %s %s 响应: %w", method, path, err)
		}
	}
	return nil
}

// listChallenges 获取题目列表及作答进度。
func (c *benchClient) listChallenges(ctx context.Context) ([]challenge, error) {
	var out []challenge
	if err := c.do(ctx, http.MethodGet, "/openapi/v1/challenges", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// start 启动一道题的靶场容器（幂等），返回容器直连地址。
func (c *benchClient) start(ctx context.Context, uniqueCode string) (startResp, error) {
	var out startResp
	path := "/openapi/v1/challenges/start?unique_code=" + url.QueryEscape(uniqueCode)
	err := c.do(ctx, http.MethodPost, path, nil, &out)
	return out, err
}

// submit 提交 flag 进行判定。
func (c *benchClient) submit(ctx context.Context, uniqueCode, flag string) (submitResp, error) {
	var out submitResp
	body := map[string]string{"unique_code": uniqueCode, "flag": flag}
	err := c.do(ctx, http.MethodPost, "/openapi/v1/challenges/submit", body, &out)
	return out, err
}

// close 关闭并释放题目容器（幂等），放出活跃题名额。
func (c *benchClient) close(ctx context.Context, uniqueCode string) (closeResp, error) {
	var out closeResp
	path := "/openapi/v1/challenges/close?unique_code=" + url.QueryEscape(uniqueCode)
	err := c.do(ctx, http.MethodPost, path, nil, &out)
	return out, err
}

// vpnPrecheck 执行 VPN 联通预检（强制前置）。probeURL 默认 http://10.0.100.58；
// 只要 status=="ok" 即算连通。该地址只在 VPN 内可达，不走 BENCHMARK_TOKEN。
func vpnPrecheck(ctx context.Context, client *http.Client, probeURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL, nil)
	if err != nil {
		return fmt.Errorf("构造 VPN 预检请求: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("VPN 预检请求失败（靶场 VPN 未连通？）: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("VPN 预检 HTTP %d（靶场 VPN 未连通？）", resp.StatusCode)
	}
	var out vpnPrecheckResp
	if err := json.Unmarshal(raw, &out); err != nil {
		return fmt.Errorf("VPN 预检响应解析失败（非靶场网关？）: %w", err)
	}
	if out.Status != "ok" {
		return fmt.Errorf("VPN 预检 status=%q（期望 ok）", out.Status)
	}
	return nil
}

// truncate 截断字符串用于错误展示。
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
