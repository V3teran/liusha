// Package sandbox 是 sandbox 组件：在本机执行 agent 工具命令的 HTTP 服务。
//
// 单镜像 / 多镜像部署时运行在自建的 Kali 工具镜像里；二进制部署时工具取决于本机安装。
// runner 经 sandbox.url + sandbox.token 调用它，api 经它查询工具安装状态。
package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/logx"
	"github.com/V3teran/liusha/internal/sandbox"
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
	workspaceDir := cfg.WorkspaceDir()
	if err := os.MkdirAll(workspaceDir, 0o750); err != nil {
		return fmt.Errorf("创建任务工作区 %s: %w", workspaceDir, err)
	}
	shell, err := resolveShell(sc.Shell, systemShellLookup())
	if err != nil {
		return err
	}
	logger.Info().Str("shell", shell).Msg("命令 shell 已就绪")

	runDir, err := runtimeDir(sc.ListenAddr)
	if err != nil {
		return err
	}
	browserScripts, err := browser.Install(runDir, sc.BrowserPython)
	if err != nil {
		return fmt.Errorf("释放浏览器脚本: %w", err)
	}
	browserDir := filepath.Join(runDir, "browser")
	if err := os.MkdirAll(browserDir, 0o700); err != nil {
		return fmt.Errorf("创建浏览器端点目录: %w", err)
	}

	srv := server.New(server.Config{
		Token:          sc.Token,
		WorkspaceDir:   workspaceDir,
		Shell:          shell,
		Tools:          manifest.Default().Names(),
		BrowserBin:     browserScripts.Bin,
		BrowserService: browserScripts.Service,
		BrowserPython:  sc.BrowserPython,
		BrowserDir:     browserDir,
		IngestURL:      strings.TrimRight(cfg.Runner.URL, "/") + sandbox.TrafficIngestPath,
		ViewportWidth:  sc.ViewportWidth,
		ViewportHeight: sc.ViewportHeight,
	})
	go server.RunWorkspaceSweep(ctx, workspaceDir, sc.WorkspaceRetentionDays)

	logger.Info().Str("addr", sc.ListenAddr).Str("workspace", workspaceDir).Msg("sandbox listening")
	if err := srv.ListenAndServe(ctx, sc.ListenAddr); err != nil {
		return fmt.Errorf("沙盒服务: %w", err)
	}
	logger.Info().Msg("sandbox stopped")
	return nil
}

// runtimeDir 是本实例的运行目录（浏览器脚本、浏览器服务端点），权限 0700，按监听地址区分：
// 同一用户可在同一台机器上运行多个沙盒，同时监听的实例地址必然不同，互不覆盖；同一地址
// 重启沿用原目录，上一实例拉起、仍在运行的浏览器服务继续可用。
func runtimeDir(listenAddr string) (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	name := strings.NewReplacer(":", "_", "/", "_", "[", "", "]", "").Replace(listenAddr)
	dir := filepath.Join(base, "liusha", "sandbox-"+name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("创建沙盒运行目录 %s: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil { // #nosec G302 // 目录须有执行位才能进入；0700 只对本用户开放
		return "", fmt.Errorf("设置沙盒运行目录权限: %w", err)
	}
	return dir, nil
}
