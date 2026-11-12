package server

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"
)

// maxIngestBody 是单条回灌流量的上限（含 base64 的请求 / 响应体）。
const maxIngestBody = 32 << 20

// ingestTimeout 是转发一条流量给 runner 的超时。
const ingestTimeout = 10 * time.Second

// listenIngestSocket 在 path 监听 unix 套接字，权限 0600（只有沙盒所属用户可连）。
// 残留的套接字文件（上次进程未正常退出）先删除。
func listenIngestSocket(path string) (net.Listener, error) {
	if path == "" {
		return nil, fmt.Errorf("配置了流量回灌但未指定本地回灌套接字")
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("清理残留回灌套接字 %s: %w", path, err)
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("监听回灌套接字 %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("设置回灌套接字权限: %w", err)
	}
	return ln, nil
}

// ingestRelay 把浏览器服务投递的流量（POST /ingest）带令牌转发给 runner，回传 runner 的状态码。
// 令牌只在本进程内：浏览器服务与 agent 命令都拿不到它。
func (s *Server) ingestRelay() http.Handler {
	client := &http.Client{Timeout: ingestTimeout}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/ingest" {
			http.NotFound(w, r)
			return
		}
		req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, s.cfg.IngestURL,
			http.MaxBytesReader(w, r.Body, maxIngestBody))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "构造转发请求: %v", err)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+s.cfg.Token)
		resp, err := client.Do(req)
		if err != nil {
			writeError(w, http.StatusBadGateway, "转发给 runner 失败: %v", err)
			return
		}
		defer func() { _ = resp.Body.Close() }()
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, io.LimitReader(resp.Body, 4096))
	})
}
