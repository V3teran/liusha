//go:build !windows

package main

import (
	"context"
	"os/signal"
	"syscall"
)

// runUntilStopped 运行 fn，收到 SIGINT / SIGTERM 时取消它的 ctx，等它返回。
func runUntilStopped(_ string, fn func(context.Context) error) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return fn(ctx)
}
