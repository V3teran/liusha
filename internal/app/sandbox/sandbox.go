// Package sandbox 是 sandbox 组件：在本机执行 agent 工具命令的 HTTP 服务。
//
// compose 部署时运行在自建的 Kali 工具镜像里；二进制部署时工具取决于本机安装。
// runner 经 sandbox.url + sandbox.token 调用它，api 经它查询工具安装状态。
package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/logx"
	"github.com/V3teran/liusha/internal/sandbox/browser"
	"github.com/V3teran/liusha/internal/sandbox/server"
	"github.com/V3teran/liusha/internal/tools/manifest"
)

// Run 运行沙盒服务直到 ctx 取消。
func Run(ctx context.Context, cfg config.AppConfig) error {
	logger := logx.New("sandbox")
	sc := cfg.Sandbox
	if sc.Token == "" {
		return errors.New("sandbox.token 未配置（环境变量 LIUSHA_SANDBOX_TOKEN）：沙盒执行任意命令，不接受无鉴权运行")
	}
	if err := os.MkdirAll(sc.WorkspaceDir, 0o750); err != nil {
		return fmt.Errorf("创建任务工作区 %s: %w", sc.WorkspaceDir, err)
	}

	scriptDir, err := browserScriptDir()
	if err != nil {
		return err
	}
	browserBin, err := browser.Install(scriptDir, sc.BrowserPython)
	if err != nil {
		return fmt.Errorf("释放浏览器脚本: %w", err)
	}

	srv := server.New(server.Config{
		Token:          sc.Token,
		WorkspaceDir:   sc.WorkspaceDir,
		Tools:          manifest.Default().Names(),
		BrowserBin:     browserBin,
		BrowserPython:  sc.BrowserPython,
		IngestURL:      sc.IngestURL,
		IngestSocket:   filepath.Join(scriptDir, "ingest.sock"),
		ViewportWidth:  sc.ViewportWidth,
		ViewportHeight: sc.ViewportHeight,
		SecretEnv:      secretEnv(cfg),
	})
	go server.RunWorkspaceSweep(ctx, sc.WorkspaceDir, sc.WorkspaceRetentionDays)

	logger.Info().Str("addr", sc.ListenAddr).Str("workspace", sc.WorkspaceDir).Msg("sandbox listening")
	if err := srv.ListenAndServe(ctx, sc.ListenAddr); err != nil {
		return fmt.Errorf("沙盒服务: %w", err)
	}
	logger.Info().Msg("sandbox stopped")
	return nil
}

// browserScriptDir 是浏览器脚本的释放目录（每次启动覆盖写入）。
func browserScriptDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	dir := filepath.Join(base, "liusha", "browser")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("创建浏览器脚本目录 %s: %w", dir, err)
	}
	return dir, nil
}

// secretEnv 列出 LIUSHA_ 前缀之外承载 liusha 密钥的环境变量，不得进入 agent 命令：
// 知识库 embedding 密钥、compose 约定的数据库 / Redis 密码、配置中 provider 声明的密钥变量。
func secretEnv(cfg config.AppConfig) []string {
	names := []string{"JINA_API_KEY", "POSTGRES_PASSWORD", "REDIS_PASSWORD"}
	for _, p := range cfg.Providers {
		if p.APIKeyEnv != "" {
			names = append(names, p.APIKeyEnv)
		}
	}
	return names
}
