// Package server 实现 sandbox-server 的 HTTP 端点。
//
// 容器内 PID 1 进程，提供：
//
//	POST /exec      跑 sh -c + OUTPUT_DIR 附件机制
//	GET  /healthz   健康检查（spawn 后等待 ready 用）
//
// 设计哲学：sandbox-server 是纯 sh 执行器，**不感知 chrome / browser-use 等
// 具体工具**——按需启动由调用者（如 browser-cli）自负。
//
// 见 docs/superpowers/specs/2026-05-16-sandbox-server-design.md
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
)

// maxLifetimeFallback 是远超任何正常 agent run 时长的安全冗余。
// 主进程崩溃 / docker stop 失败时由 lifetime guard 兜底自杀，容器随之销毁。
//
// 不是 idle timeout——agent 思考期无 /exec 请求不触发自杀，避免误杀。
const maxLifetimeFallback = 4 * time.Hour

// 附件上限：单文件无上限（避免视口截图 200KB+ 被丢），
// 仅总量 10MB + 数量 5 兜底防一次返巨量文件撑爆 HTTP body。
//
// 单文件不限的根据：browser-use screenshot 默认 viewport-only
// page.screenshot(full_page=False) 同款），典型 200-500KB；DOM 大的复杂页
// 1-2MB 也合理。要更严的总量控制由 LLM 自己注意命名/数量。
const (
	maxTotalSize = 10 * 1024 * 1024 // 总 10MB
	maxFileCount = 5
)

// Server 持有 HTTP 路由 + 启动时间（max lifetime 计算用）。
type Server struct {
	mux       *http.ServeMux
	startTime time.Time
}

// New 构造 Server，注册端点。
func New() *Server {
	s := &Server{
		mux:       http.NewServeMux(),
		startTime: time.Now(),
	}
	// Go 1.22+ 路由 method+path 语法。
	s.mux.HandleFunc("POST /exec", s.handleExec)
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	return s
}

// ListenAndServe 启动 HTTP server 并开启 max lifetime 自杀 goroutine。
//
// ctx 被取消时 server 优雅关闭（10s 超时）。
//
// 不设 ReadTimeout/WriteTimeout：单工具最长 30min，HTTP 同步长连接由主进程 client
// 端 timeout 兜底（31min）。
func (s *Server) ListenAndServe(ctx context.Context, addr string) error {
	go s.runLifetimeGuard(ctx)

	srv := &http.Server{
		Addr:    addr,
		Handler: s.mux,
	}

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("graceful shutdown: %w", err)
		}
		return nil
	case err := <-serveErr:
		if err == http.ErrServerClosed {
			return nil
		}
		return fmt.Errorf("http server: %w", err)
	}
}

// runLifetimeGuard 每分钟检查启动时长，超过 maxLifetimeFallback 直接退出进程
// （容器随 PID 1 死亡被一起回收）。
//
// 直接 os.Exit 不走 graceful shutdown：场景是主进程已死（孤儿容器），无 client
// 在等响应，立即退出最干净。
func (s *Server) runLifetimeGuard(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if time.Since(s.startTime) > maxLifetimeFallback {
				fmt.Fprintf(os.Stderr, "[sandbox-server] max lifetime %v exceeded, exiting\n", maxLifetimeFallback)
				os.Exit(1)
			}
		}
	}
}

// handleHealthz 返回 200 + {"status":"ok"}，无副作用。
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

// writeError 写 plain text 错误响应。
func writeError(w http.ResponseWriter, code int, format string, args ...any) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(code)
	fmt.Fprintf(w, format, args...)
}

// writeJSON 写 JSON 响应。
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
