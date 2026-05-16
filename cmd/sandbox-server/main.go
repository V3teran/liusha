// liusha sandbox-server 是 sandbox 容器内的 PID 1 进程，提供主进程到容器
// 内 sh 命令执行的 HTTP RPC 入口。
//
// 容器内部架构：
//
//	sandbox-server (PID 1, 监听 :8080)
//	  ├─ POST /exec     跑 sh -c + OUTPUT_DIR 附件机制
//	  └─ GET  /healthz  健康检查
//
//	chrome (browser-cli 按需 detached spawn 的子进程，监听 :9222 CDP)
//
//	/usr/local/bin/
//	  ├─ browser-cli   Python 脚本，包装 browser-use
//	  └─ sqlmap / curl / nuclei / ...
//
// sandbox-server 完全不感知 chrome / browser-use 等具体工具——按需启动由
// 调用者（如 browser-cli）自负。
//
// 见 docs/superpowers/specs/2026-05-16-sandbox-server-design.md
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/V3teran/liusha/internal/sandbox/server"
)

func main() {
	addr := os.Getenv("SANDBOX_SERVER_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	srv := server.New()
	log.Printf("[sandbox-server] listening on %s", addr)
	if err := srv.ListenAndServe(ctx, addr); err != nil {
		log.Fatalf("[sandbox-server] %v", err)
	}
	log.Printf("[sandbox-server] shutdown clean")
}
