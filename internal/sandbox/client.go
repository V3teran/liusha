package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client 是主进程到 sandbox 容器的 HTTP RPC client。
//
// 一个 Client 实例对应一个 sandbox 容器（一次 agent run）；
// Launcher.Spawn 返回 Client，run 结束 Launcher.Destroy 销毁容器。
//
// 见 docs/superpowers/specs/2026-05-16-sandbox-server-design.md
type Client interface {
	Exec(ctx context.Context, req ExecRequest) (ExecResult, error)
	Close() error
}

// httpClientTimeout 比 sandbox-server 端单工具最大 timeout（1800s = 30min）
// 稍大，给 HTTP 协议本身留余量（防止 client 比 server 先 timeout）。
const httpClientTimeout = 31 * time.Minute

// httpClient 是 Client 接口的 HTTP/JSON 实现。
type httpClient struct {
	baseURL string
	httpc   *http.Client
}

// newHTTPClient 构造绑定到指定 baseURL 的 HTTP client。
// baseURL 形如 "http://127.0.0.1:54321"（由 Launcher 通过 docker port 拿到）。
func newHTTPClient(baseURL string) *httpClient {
	return &httpClient{
		baseURL: baseURL,
		httpc: &http.Client{
			Timeout: httpClientTimeout,
		},
	}
}

// Exec POST /exec，同步等待响应。
//
// ctx 被取消时 HTTP request 立即中断；sandbox-server 端收到 r.Context().Done()
// 也会 SIGKILL 子进程——传播链是完整的。
func (c *httpClient) Exec(ctx context.Context, req ExecRequest) (ExecResult, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return ExecResult{}, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/exec", bytes.NewReader(body))
	if err != nil {
		return ExecResult{}, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpc.Do(httpReq)
	if err != nil {
		return ExecResult{}, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return ExecResult{}, fmt.Errorf("sandbox /exec: status %d: %s", resp.StatusCode, string(b))
	}

	var res ExecResult
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return ExecResult{}, fmt.Errorf("decode response: %w", err)
	}
	return res, nil
}

// Close 当前是 no-op（HTTP client 不需要显式关闭）。
// 保留接口为未来扩展（连接池清理 / 取消挂起请求等）留口子。
func (c *httpClient) Close() error {
	return nil
}
