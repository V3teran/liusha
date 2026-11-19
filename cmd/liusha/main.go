// liusha 是多 agent 协作的自动化 Web 安全测试平台的唯一程序：各组件是它的子命令。
//
//	liusha serve          同一进程运行全部组件（二进制与单镜像部署）
//	liusha api|proxy|runner|sandbox
//	                      分别运行单个组件（compose 部署，每个容器一个）
//	liusha migrate        把数据库迁移到最新版本并同步出厂内容（内置技能与场景）
//	liusha corpus-import  向知识库导入专家知识
//	liusha version        打印版本
//
// 子命令都接受：
//   - -env-file <file>：先读入其中的环境变量（已设置的环境变量优先）。二进制部署用它读与程序
//     放在一起的 .env（由 .env.example 复制填写）；只在显式给出时读，容器里的环境变量由容器注入
//   - -config <file>：配置文件，必须存在且列出全部键。不给时取环境变量 LIUSHA_CONFIG（容器镜像
//     设为挂载进来的 /data/config.yaml），再不给就是程序所在目录的 config.yaml（二进制部署：
//     liusha、config.yaml、.env 放在同一目录）
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/V3teran/liusha/internal/app"
	"github.com/V3teran/liusha/internal/app/api"
	"github.com/V3teran/liusha/internal/app/corpusimport"
	appmigrate "github.com/V3teran/liusha/internal/app/migrate"
	"github.com/V3teran/liusha/internal/app/proxy"
	"github.com/V3teran/liusha/internal/app/runner"
	"github.com/V3teran/liusha/internal/app/sandbox"
	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/envfile"
	"github.com/V3teran/liusha/internal/logx"
)

// version 由发布构建经 -ldflags "-X main.version=<版本>" 注入。
var version = "dev"

// command 是一个子命令。
type command struct {
	name    string
	summary string
	run     func(ctx context.Context, cfg config.AppConfig, args []string) error
}

var commands = []command{
	{name: "serve", summary: "同一进程运行全部组件", run: serve},
	{name: "api", summary: "运行 api 组件（HTTP API 与控制台）", run: component(api.Run)},
	{name: "proxy", summary: "运行 proxy 组件（MITM 代理入口）", run: component(proxy.Run)},
	{name: "runner", summary: "运行 runner 组件（任务执行）", run: component(runner.Run)},
	{name: "sandbox", summary: "运行 sandbox 组件（工具执行沙盒）", run: component(sandbox.Run)},
	{name: "migrate", summary: "把数据库迁移到最新版本并同步出厂内容", run: migrate},
	{name: "corpus-import", summary: "向知识库导入专家知识：corpus-import <file.md>...", run: corpusimport.Run},
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
	}
	cmd, ok := lookup(name)
	if !ok {
		_, _ = fmt.Fprintf(stderr, "未知命令 %q\n\n", name)
		usage(stderr)
		return 2
	}

	fs := flag.NewFlagSet("liusha "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	envFile := fs.String("env-file", "", "先读入的环境变量文件（如程序旁的 .env；已设置的环境变量优先）")
	configPath := fs.String("config", "", "配置文件（不给时取 LIUSHA_CONFIG，再不给是程序所在目录的 config.yaml）")
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
	if *configPath == "" {
		p, err := besideExecutable("config.yaml")
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "liusha %s: %v\n", name, err)
			return 1
		}
		*configPath = p
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "liusha %s: 加载配置: %v\n", name, err)
		return 1
	}
	// 日志按配置输出，文件按子命令命名（<data_dir>/logs/<命令>.log），须先于任何日志写出。
	logx.SetProcess(name)
	logx.Configure(logx.Options{
		Level:        cfg.Log.Level,
		Format:       cfg.Log.Format,
		ToStdout:     cfg.Log.ToStdout,
		ToFile:       cfg.Log.ToFile,
		Dir:          cfg.LogDir(),
		MaxSizeBytes: cfg.Log.MaxSizeBytes,
		MaxBackups:   cfg.Log.MaxBackups,
		MaxAgeDays:   cfg.Log.MaxAgeDays,
		Compress:     cfg.Log.Compress,
	})

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
	for _, c := range commands {
		_, _ = fmt.Fprintf(w, "  %-14s %s\n", c.name, c.summary)
	}
	_, _ = fmt.Fprintf(w, "  %-14s %s\n", "version", "打印版本")
}

// besideExecutable 返回与程序同目录的文件路径（跟随符号链接到程序本体所在目录）。
// 按程序位置而非工作目录定位：以服务方式运行时的工作目录不可控（Windows 服务是 System32）。
func besideExecutable(name string) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("定位程序所在目录: %w", err)
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return "", fmt.Errorf("定位程序所在目录: %w", err)
	}
	return filepath.Join(filepath.Dir(exe), name), nil
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
	return appmigrate.Run(ctx, cfg)
}

// serve 先把数据库迁移到最新版本并同步出厂内容（单机部署替换程序即升级），再在同一进程运行全部组件。
func serve(ctx context.Context, cfg config.AppConfig, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("不接受参数 %q", args)
	}
	if err := appmigrate.Run(ctx, cfg); err != nil {
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
