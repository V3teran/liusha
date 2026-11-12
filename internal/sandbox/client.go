package sandbox

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

// Client 是沙盒服务的调用接口。
type Client interface {
	// Exec 执行一条命令并同步等待结果。ctx 取消即中断请求，沙盒随之杀掉该命令的进程组。
	Exec(ctx context.Context, req ExecRequest) (ExecResult, error)
	// Inventory 返回沙盒实际安装的工具与浏览器能力。
	Inventory(ctx context.Context) (Inventory, error)
	// Release 结束任务在沙盒里遗留的全部进程与浏览器会话（只动该任务，不影响并发任务）。
	Release(ctx context.Context, taskID string) (ReleaseResult, error)
}

// execTimeoutMargin 是 HTTP 层超时相对命令超时的余量：命令超时由沙盒执行，客户端只为
// 沙盒失联兜底，不能先于沙盒放弃。
const execTimeoutMargin = time.Minute

// controlTimeout 是 Inventory / Release 等控制类请求的超时。
const controlTimeout = 30 * time.Second

// HTTPClient 是 Client 的 HTTP/JSON 实现。
type HTTPClient struct {
	baseURL string
	token   string
	httpc   *http.Client
}

// NewClient 构造访问 baseURL（如 http://127.0.0.1:8591）的客户端，token 为共享密钥。
func NewClient(baseURL, token string) *HTTPClient {
	return &HTTPClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		httpc:   &http.Client{}, // 超时按请求设定（命令时长差异很大）
	}
}

// Exec 实现 Client。
func (c *HTTPClient) Exec(ctx context.Context, req ExecRequest) (ExecResult, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(req.TimeoutSeconds)*time.Second+execTimeoutMargin)
	defer cancel()
	var res ExecResult
	if err := c.do(ctx, http.MethodPost, "/exec", req, &res); err != nil {
		return ExecResult{}, err
	}
	return res, nil
}

// Inventory 实现 Client。
func (c *HTTPClient) Inventory(ctx context.Context) (Inventory, error) {
	ctx, cancel := context.WithTimeout(ctx, controlTimeout)
	defer cancel()
	var inv Inventory
	if err := c.do(ctx, http.MethodGet, "/tools", nil, &inv); err != nil {
		return Inventory{}, err
	}
	return inv, nil
}

// Release 实现 Client。
func (c *HTTPClient) Release(ctx context.Context, taskID string) (ReleaseResult, error) {
	ctx, cancel := context.WithTimeout(ctx, controlTimeout)
	defer cancel()
	var res ReleaseResult
	if err := c.do(ctx, http.MethodPost, "/tasks/"+url.PathEscape(taskID)+"/release", nil, &res); err != nil {
		return ReleaseResult{}, err
	}
	return res, nil
}

func (c *HTTPClient) do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("编码请求: %w", err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rd)
	if err != nil {
		return fmt.Errorf("构造请求: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.httpc.Do(req)
	if err != nil {
		return fmt.Errorf("沙盒 %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("沙盒 %s %s: 状态 %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("沙盒 %s %s: 解码响应: %w", method, path, err)
	}
	return nil
}
