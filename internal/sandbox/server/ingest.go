package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// maxIngestBody 是单条回灌流量的上限（含 base64 的请求 / 响应体）。
const maxIngestBody = 32 << 20

// ingestTimeout 是转发一条流量给 runner 的超时。
const ingestTimeout = 10 * time.Second

// ingestRelay 是浏览器服务投递流量的本机中继：只监听回环地址，凭中继口令接收，再带沙盒
// 令牌转发给 runner。沙盒令牌只在本进程内，浏览器服务与 agent 命令都拿不到它；中继口令
// 随命令环境下发——同用户的命令本就能投递（它们驱动的浏览器服务即如此），口令挡的是本机
// 其他用户（看不到别人进程的环境）。回环 TCP 而不是 unix 套接字：全平台同一机制（Windows
// 上的 Python 没有 unix 套接字）。
type ingestRelay struct {
	url string // http://127.0.0.1:<端口>/ingest；未启用时为空
	key string
}

// newRelayKey 生成中继口令（进程级，每次启动重新生成）。
func newRelayKey() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("生成中继口令: %v", err)) // 系统随机源不可用，无从安全运行
	}
	return hex.EncodeToString(b)
}

// listenRelay 在回环地址的随机端口监听中继。
func listenRelay() (net.Listener, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("监听回灌中继: %w", err)
	}
	return ln, nil
}

// relayHandler 校验中继口令，把流量（POST /ingest）带沙盒令牌转发给 runner，回传 runner 的状态码。
func (s *Server) relayHandler() http.Handler {
	client := &http.Client{Timeout: ingestTimeout}
	want := []byte(s.relay.key)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/ingest" {
			http.NotFound(w, r)
			return
		}
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || subtle.ConstantTimeCompare([]byte(got), want) != 1 {
			writeError(w, http.StatusUnauthorized, "中继口令无效")
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
