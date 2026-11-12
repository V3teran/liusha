// Package server 实现沙盒服务（liusha sandbox）的 HTTP 端点。
//
//	GET  /healthz              健康检查（无需令牌）
//	POST /exec                 执行一条 sh -c 命令（$OUTPUT_DIR 产物作为附件返回）
//	GET  /tools                工具清单中各 CLI 工具与浏览器能力的安装状态
//	POST /tasks/{id}/release   结束任务遗留的全部进程组与浏览器会话
//
// 除 /healthz 外都要求 Authorization: Bearer <令牌>。
//
// 另有一个本地 unix 套接字（Config.IngestSocket）供沙盒内的浏览器服务回灌流量：沙盒带令牌
// 转发给 runner，令牌只在沙盒进程内，不进入任何命令的环境。
package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// 附件上限：单文件不限（视口截图 200KB+、复杂页 1-2MB 都合理），
// 仅总量 10MB + 数量 5 兜底，防一次返回巨量文件撑爆 HTTP 响应。
const (
	maxTotalSize = 10 * 1024 * 1024
	maxFileCount = 5
)

// Config 是沙盒服务参数。
type Config struct {
	// Token 是共享密钥：调用方须携带它；沙盒转发浏览器流量给 runner 时也用它。
	Token string
	// WorkspaceDir 是任务工作区根目录（<根>/<批次>/<任务>/{work,out,state}）。
	WorkspaceDir string
	// Tools 是工具清单中的 CLI 工具名（GET /tools 的探测范围）。
	Tools []string
	// BrowserBin 是浏览器包装脚本所在目录，加到命令 PATH 最前；BrowserPython 是其解释器（能力探测用）。
	BrowserBin    string
	BrowserPython string
	// IngestURL 是浏览器抓到的流量回灌 runner 的地址；空 = 不回灌。
	IngestURL string
	// IngestSocket 是浏览器服务投递流量的本地 unix 套接字路径（IngestURL 非空时必填）。
	IngestSocket string
	// ViewportWidth/Height 是浏览器视口尺寸。
	ViewportWidth  int
	ViewportHeight int
	// SecretEnv 是除 LIUSHA_ 前缀外、承载 liusha 密钥的环境变量名，不得进入命令环境（见 scrubEnv）。
	SecretEnv []string
}

// Server 是沙盒服务。
type Server struct {
	cfg       Config
	mux       *http.ServeMux
	procs     *taskProcesses
	inventory *inventoryCache
}

// New 构造沙盒服务。
func New(cfg Config) *Server {
	s := &Server{
		cfg:       cfg,
		mux:       http.NewServeMux(),
		procs:     newTaskProcesses(),
		inventory: newInventoryCache(cfg.Tools, cfg.BrowserPython),
	}
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	s.mux.Handle("POST /exec", s.requireToken(http.HandlerFunc(s.handleExec)))
	s.mux.Handle("GET /tools", s.requireToken(http.HandlerFunc(s.handleTools)))
	s.mux.Handle("POST /tasks/{id}/release", s.requireToken(http.HandlerFunc(s.handleRelease)))
	return s
}

// Handler 返回 HTTP 处理器（测试与自定义 http.Server 用）。
func (s *Server) Handler() http.Handler { return s.mux }

// ListenAndServe 监听 addr（与配置了回灌时的本地回灌套接字）直到 ctx 取消，然后优雅关闭（10s 超时）。
//
// 不设 ReadTimeout/WriteTimeout：命令可长达数十分钟，HTTP 同步长连接由调用方按命令超时兜底。
func (s *Server) ListenAndServe(ctx context.Context, addr string) error {
	servers := []*http.Server{{Addr: addr, Handler: s.mux, ReadHeaderTimeout: 10 * time.Second}}
	listeners := []net.Listener{nil}
	if s.cfg.IngestURL != "" {
		ln, err := listenIngestSocket(s.cfg.IngestSocket)
		if err != nil {
			return err
		}
		servers = append(servers, &http.Server{Handler: s.ingestRelay(), ReadHeaderTimeout: 10 * time.Second})
		listeners = append(listeners, ln)
	}

	serveErr := make(chan error, len(servers))
	for i, srv := range servers {
		go func() {
			if listeners[i] == nil {
				serveErr <- srv.ListenAndServe()
				return
			}
			serveErr <- srv.Serve(listeners[i])
		}()
	}

	var runErr error
	select {
	case <-ctx.Done():
	case err := <-serveErr:
		runErr = fmt.Errorf("http server: %w", err)
	}
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	for _, srv := range servers {
		if err := srv.Shutdown(shutdownCtx); err != nil && runErr == nil {
			runErr = fmt.Errorf("graceful shutdown: %w", err)
		}
	}
	if s.cfg.IngestSocket != "" {
		_ = os.Remove(s.cfg.IngestSocket)
	}
	return runErr
}

// requireToken 校验 Bearer 令牌（常量时间比较，不泄露前缀匹配长度）。
func (s *Server) requireToken(next http.Handler) http.Handler {
	want := []byte(s.cfg.Token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || len(want) == 0 || subtle.ConstantTimeCompare([]byte(got), want) != 1 {
			writeError(w, http.StatusUnauthorized, "令牌无效")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleTools(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.inventory.get(r.Context()))
}

// writeError 写 plain text 错误响应。
func writeError(w http.ResponseWriter, code int, format string, args ...any) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(code)
	_, _ = fmt.Fprintf(w, format, args...)
}

// writeJSON 写 JSON 响应。
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
