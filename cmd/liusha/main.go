// liusha 是多 agent 协作的自动化 Web 安全测试平台的唯一程序：各组件是它的子命令。
//
//	liusha setup          二进制部署首装：迁移数据库、生成密钥与环境变量文件
//	liusha serve          同一进程运行全部组件（二进制与单镜像部署）
//	liusha api|proxy|runner|sandbox
//	                      分别运行单个组件（compose 部署，每个容器一个）
//	liusha migrate        把数据库迁移到最新版本
//	liusha corpus-import  向知识库导入专家知识
//	liusha version        打印版本
//
// 除 setup 外的子命令都接受：
//   - -env-file <file>：先读入其中的环境变量（已设置的环境变量优先），即 setup 生成的文件
//   - -config <file>（缺省取环境变量 LIUSHA_CONFIG）：只需写要覆盖内置默认的键
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/V3teran/liusha/internal/app"
	"github.com/V3teran/liusha/internal/app/api"
	"github.com/V3teran/liusha/internal/app/corpusimport"
	"github.com/V3teran/liusha/internal/app/proxy"
	"github.com/V3teran/liusha/internal/app/runner"
	"github.com/V3teran/liusha/internal/app/sandbox"
	"github.com/V3teran/liusha/internal/app/setup"
	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/db"
	"github.com/V3teran/liusha/internal/envfile"
	"github.com/V3teran/liusha/internal/logx"
)

// version 由发布构建经 -ldflags "-X main.version=<版本>" 注入。
var version = "dev"

// command 是一个子命令。
type command struct {
	name    string
	summary string
	// withLLM：组件会调 LLM，加载配置时校验 LLM 种子形状。
	withLLM bool
	run     func(ctx context.Context, cfg config.AppConfig, args []string) error
}

var commands = []command{
	{name: "serve", summary: "同一进程运行全部组件", withLLM: true, run: serve},
	{name: "api", summary: "运行 api 组件（HTTP API 与控制台）", withLLM: true, run: component(api.Run)},
	{name: "proxy", summary: "运行 proxy 组件（MITM 代理入口）", run: component(proxy.Run)},
	{name: "runner", summary: "运行 runner 组件（任务执行）", withLLM: true, run: component(runner.Run)},
	{name: "sandbox", summary: "运行 sandbox 组件（工具执行沙盒）", run: component(sandbox.Run)},
	{name: "migrate", summary: "把数据库迁移到最新版本", run: migrate},
	{name: "corpus-import", summary: "向知识库导入专家知识：corpus-import <file.md>...", withLLM: true, run: corpusimport.Run},
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run 解析子命令并执行，返回进程退出码：0 成功，1 运行失败，2 用法错误。
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	name := args[0]
	switch name {
	case "-h", "-help", "--help", "help":
		usage(stdout)
		return 0
	case "version", "-version", "--version":
		_, _ = fmt.Fprintln(stdout, version)
		return 0
	case "setup":
		return runSetup(args[1:], stdout, stderr)
	}
	cmd, ok := lookup(name)
	if !ok {
		_, _ = fmt.Fprintf(stderr, "未知命令 %q\n\n", name)
		usage(stderr)
		return 2
	}

	fs := flag.NewFlagSet("liusha "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	envFile := fs.String("env-file", "", "先读入的环境变量文件（liusha setup 生成；已设置的环境变量优先）")
	configPath := fs.String("config", "", "覆盖内置默认的配置文件（只需写要改的键；缺省取 LIUSHA_CONFIG）")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *envFile != "" {
		if err := envfile.Load(*envFile); err != nil {
			_, _ = fmt.Fprintf(stderr, "liusha %s: %v\n", name, err)
			return 1
		}
	}
	if *configPath == "" {
		*configPath = os.Getenv("LIUSHA_CONFIG")
	}

	// 日志文件按子命令命名（logs/<命令>.log），须先于任何日志写出。
	logx.SetProcess(name)

	load := config.LoadWithoutLLM
	if cmd.withLLM {
		load = config.Load
	}
	cfg, err := load(*configPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "liusha %s: 加载配置: %v\n", name, err)
		return 1
	}

	if err := runUntilStopped(name, func(ctx context.Context) error { return cmd.run(ctx, cfg, fs.Args()) }); err != nil {
		_, _ = fmt.Fprintf(stderr, "liusha %s: %v\n", name, err)
		return 1
	}
	return 0
}

func lookup(name string) (command, bool) {
	for _, c := range commands {
		if c.name == name {
			return c, true
		}
	}
	return command{}, false
}

func usage(w io.Writer) {
	_, _ = fmt.Fprintf(w, "liusha %s\n\n用法：liusha <命令> [-env-file 文件] [-config 文件] [参数]\n\n命令：\n", version)
	_, _ = fmt.Fprintf(w, "  %-14s %s\n", "setup", "二进制部署首装：迁移数据库、生成密钥与环境变量文件")
	for _, c := range commands {
		_, _ = fmt.Fprintf(w, "  %-14s %s\n", c.name, c.summary)
	}
	_, _ = fmt.Fprintf(w, "  %-14s %s\n", "version", "打印版本")
}

// runSetup 解析首装参数并执行。它生成 -env-file，因此不走其他子命令的配置加载流程。
func runSetup(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("liusha setup", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var opts setup.Options
	fs.StringVar(&opts.EnvFile, "env-file", "liusha.env", "要生成的环境变量文件")
	fs.StringVar(&opts.PostgresDSN, "postgres-dsn", os.Getenv("LIUSHA_POSTGRES_DSN"), "PostgreSQL 连接串（库需已安装 pgvector 扩展）")
	fs.StringVar(&opts.RedisAddr, "redis-addr", envOr("LIUSHA_REDIS_ADDR", "127.0.0.1:6379"), "Redis 地址")
	fs.StringVar(&opts.RedisPassword, "redis-password", os.Getenv("LIUSHA_REDIS_PASSWORD"), "Redis 密码（无密码留空）")
	fs.StringVar(&opts.WorkspaceDir, "workspace-dir", "", "任务工作区目录（缺省 ~/data/liusha/workspace）")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		_, _ = fmt.Fprintf(stderr, "liusha setup: 不接受参数 %q\n", fs.Args())
		return 2
	}
	logx.SetProcess("setup")
	if err := runUntilStopped("setup", func(ctx context.Context) error { return setup.Run(ctx, opts, stdout) }); err != nil {
		_, _ = fmt.Fprintf(stderr, "liusha setup: %v\n", err)
		return 1
	}
	return 0
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// component 把组件的 Run 适配为子命令（组件不接受位置参数）。
func component(run func(context.Context, config.AppConfig) error) func(context.Context, config.AppConfig, []string) error {
	return func(ctx context.Context, cfg config.AppConfig, args []string) error {
		if len(args) > 0 {
			return fmt.Errorf("不接受参数 %q", args)
		}
		return run(ctx, cfg)
	}
}

func migrate(ctx context.Context, cfg config.AppConfig, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("不接受参数 %q", args)
	}
	return db.Migrate(ctx, cfg.Postgres.DSN, logx.New("migrate"))
}

// serve 先把数据库迁移到最新版本（单机部署替换程序即升级），再在同一进程运行全部组件。
func serve(ctx context.Context, cfg config.AppConfig, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("不接受参数 %q", args)
	}
	if err := db.Migrate(ctx, cfg.Postgres.DSN, logx.New("migrate")); err != nil {
		return err
	}
	bind := func(name string, run func(context.Context, config.AppConfig) error) app.Component {
		return app.Component{Name: name, Run: func(ctx context.Context) error { return run(ctx, cfg) }}
	}
	// 沙盒在底层：runner 停机排空在途任务期间，沙盒仍在服务。
	return app.Serve(ctx,
		[]app.Component{bind("sandbox", sandbox.Run)},
		[]app.Component{bind("api", api.Run), bind("proxy", proxy.Run), bind("runner", runner.Run)},
	)
}
