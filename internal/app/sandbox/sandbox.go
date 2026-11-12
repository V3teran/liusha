// Package sandbox 是 sandbox 组件：在本机执行工具命令的 HTTP 服务（POST /exec、GET /healthz）。
package sandbox

import (
	"context"
	"fmt"

	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/logx"
	"github.com/V3teran/liusha/internal/sandbox/server"
)

// Run 运行沙盒服务直到 ctx 取消。
func Run(ctx context.Context, cfg config.AppConfig) error {
	logger := logx.New("sandbox")
	logger.Info().Str("addr", cfg.Sandbox.ListenAddr).Msg("sandbox listening")
	if err := server.New().ListenAndServe(ctx, cfg.Sandbox.ListenAddr); err != nil {
		return fmt.Errorf("沙盒服务: %w", err)
	}
	logger.Info().Msg("sandbox stopped")
	return nil
}
