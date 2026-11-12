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
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/db"
	"github.com/V3teran/liusha/internal/llmconfig"
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
	shell, err := exec.LookPath(sc.Shell)
	if err != nil {
		return fmt.Errorf("找不到执行命令的 POSIX shell %q（sandbox.shell）: %w；Windows 上需安装提供 sh 的环境（如 Git for Windows），并把 sandbox.shell 指向其 sh.exe", sc.Shell, err)
	}
	secrets, err := secretEnv(ctx, cfg)
	if err != nil {
		return err
	}

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
		WorkspaceDir:   sc.WorkspaceDir,
		Shell:          shell,
		Tools:          manifest.Default().Names(),
		BrowserBin:     browserScripts.Bin,
		BrowserService: browserScripts.Service,
		BrowserPython:  sc.BrowserPython,
		BrowserDir:     browserDir,
		IngestURL:      sc.IngestURL,
		ViewportWidth:  sc.ViewportWidth,
		ViewportHeight: sc.ViewportHeight,
		SecretEnv:      secrets,
	})
	go server.RunWorkspaceSweep(ctx, sc.WorkspaceDir, sc.WorkspaceRetentionDays)

	logger.Info().Str("addr", sc.ListenAddr).Str("workspace", sc.WorkspaceDir).Msg("sandbox listening")
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

// secretEnv 列出 LIUSHA_ 前缀之外承载 liusha 密钥的环境变量，不得进入 agent 命令：知识库
// embedding 密钥、compose 约定的数据库 / Redis 密码，以及 LLM provider 声明的密钥变量。
//
// provider 的密钥变量名（api_key_env）由首装种子写入数据库，之后配置里可能不再声明，而它指向
// 的变量仍在进程环境里（serve 同进程、二进制部署的环境变量文件）。配置了数据库时从库里读全部
// provider 的声明；读不到则拒绝启动——宁可不运行，也不让 LLM 编写的命令看到 LLM 密钥。
// compose 部署的沙盒不连数据库，其环境里本就没有这些变量。
func secretEnv(ctx context.Context, cfg config.AppConfig) ([]string, error) {
	names := []string{"JINA_API_KEY", "POSTGRES_PASSWORD", "REDIS_PASSWORD"}
	for _, p := range cfg.Providers {
		if p.APIKeyEnv != "" {
			names = append(names, p.APIKeyEnv)
		}
	}
	if cfg.Postgres.DSN == "" {
		return names, nil
	}
	pool, err := db.NewPgPool(ctx, cfg.Postgres, logx.New("sandbox"))
	if err != nil {
		return nil, fmt.Errorf("读取 LLM provider 的密钥变量名（连接数据库）: %w", err)
	}
	defer pool.Close()
	providers, err := llmconfig.NewStore(pool).ListProviders(ctx, false)
	if err != nil {
		return nil, fmt.Errorf("读取 LLM provider 的密钥变量名: %w", err)
	}
	for _, p := range providers {
		if p.APIKeyEnv != "" {
			names = append(names, p.APIKeyEnv)
		}
	}
	return names, nil
}
