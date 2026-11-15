// Package browser 是沙盒的浏览器能力：浏览器服务脚本（client.py / service.py）编进程序，
// 沙盒启动时释放到本地目录；按任务关闭浏览器会话；探测浏览器能力是否可用。
//
// 每个任务的每个浏览器身份一个常驻服务进程，监听回环地址的随机端口，凭随机口令接收命令；
// 端口与口令写在端点目录（沙盒运行目录下，只对本用户开放）的 <任务ID>/<身份>.json 里，约定与
// client.py / service.py 一致。身份只在任务内有意义（同名身份在不同任务里是不同的浏览器），
// 任务归属由目录表达。回环 TCP 而不是 unix 套接字：全平台同一机制（Windows 上的 Python
// 没有 unix 套接字）。drive_browser 工具在沙盒里执行 `browser-use <子命令>`，由本包生成的包装
// 脚本以 sandbox.browser_python 运行 client.py。
package browser

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

//go:embed client.py service.py
var scripts embed.FS

// Command 是浏览器能力的命令入口（Install 生成的包装脚本名，也是工具清单里的 CLI 工具名）。
const Command = "browser-use"

// probeImports 是浏览器服务（service.py）依赖的全部第三方导入：能力探测原样导入它们，
// 库升级移除了任一名称都会如实判为不可用（单测守住它与 service.py 一致）。
const probeImports = `import httpx
from browser_use import BrowserSession
from browser_use.actor.page import Page
from browser_use.browser.events import NavigateToUrlEvent
from browser_use.dom.serializer.serializer import DOMTreeSerializer`

// Scripts 是释放到本地的浏览器脚本。
type Scripts struct {
	Bin     string // browser-use 包装脚本所在目录，加到命令 PATH 最前
	Service string // 浏览器服务脚本（沙盒以 sandbox.browser_python 运行它）
}

// Install 把浏览器脚本释放到 dir，并在 dir/bin 生成以 python 运行客户端的 browser-use 包装脚本
// （sh 脚本：命令由沙盒的 POSIX shell 执行）。每次启动覆盖写入，脚本随程序版本更新。
func Install(dir, python string) (Scripts, error) {
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o750); err != nil {
		return Scripts{}, fmt.Errorf("创建浏览器脚本目录: %w", err)
	}
	for _, name := range []string{"client.py", "service.py"} {
		b, err := scripts.ReadFile(name)
		if err != nil {
			return Scripts{}, fmt.Errorf("读取内置脚本 %s: %w", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o640); err != nil { // #nosec G306 // 只读脚本，解释器读取即可
			return Scripts{}, fmt.Errorf("释放 %s: %w", name, err)
		}
	}
	wrapper := wrapperScript(python, filepath.Join(dir, "client.py"))
	if err := os.WriteFile(filepath.Join(binDir, Command), []byte(wrapper), 0o750); err != nil { // #nosec G306 // 包装脚本须可执行
		return Scripts{}, fmt.Errorf("生成 browser-use 包装脚本: %w", err)
	}
	return Scripts{Bin: binDir, Service: filepath.Join(dir, "service.py")}, nil
}

// Status 是浏览器能力的探测结果。
type Status struct {
	Installed bool
	Detail    string // 未安装时说明缺什么
}

// probeTimeout 是一次能力探测的上限（导入 browser_use 依赖较多，冷启动要几秒）。
const probeTimeout = 30 * time.Second

// Probe 探测浏览器能力：python 能导入浏览器服务用到的全部名称即视为可用（chromium 由
// browser_use 自行定位或安装，无法在不启动浏览器的前提下可靠探测）。
func Probe(ctx context.Context, python string) Status {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	if _, err := exec.LookPath(python); err != nil {
		return Status{Detail: fmt.Sprintf("找不到 Python 解释器 %q（配置 sandbox.browser_python）", python)}
	}
	out, err := exec.CommandContext(ctx, python, "-c", probeImports).CombinedOutput() // #nosec G204 // 解释器来自部署配置
	if err != nil {
		return Status{Detail: fmt.Sprintf("%s 缺少浏览器服务依赖（需 browser-use 0.13.x）: %s", python, lastLine(out, err))}
	}
	return Status{Installed: true}
}

// resetTimeout 是关闭单个浏览器会话的等待上限（关闭 chromium 通常在秒级）。
const resetTimeout = 15 * time.Second

// wrapperScript 生成 browser-use 包装脚本。路径写成正斜杠形式（sh 里反斜杠是转义符）。
// Windows 上的 sh 在调用原生程序时会把形似路径的参数改写成 Windows 路径，浏览器参数（URL、
// 选择器、脚本）不是路径，须原样交给客户端，故关掉这一改写。
func wrapperScript(python, client string) string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	if runtime.GOOS == "windows" {
		b.WriteString("export MSYS_NO_PATHCONV=1 MSYS2_ARG_CONV_EXCL='*'\n")
	}
	fmt.Fprintf(&b, "exec %s %s \"$@\"\n", shellQuote(filepath.ToSlash(python)), shellQuote(filepath.ToSlash(client)))
	return b.String()
}

// ValidIdentity 报告身份名是否合法：[A-Za-z0-9_-]{1,64}。身份名是端点文件名，也是浏览器配置
// 目录名（client.py 按同一规则先行校验）。
func ValidIdentity(name string) bool {
	if len(name) == 0 || len(name) > 64 {
		return false
	}
	for i := 0; i < len(name); i++ {
		switch c := name[i]; {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

// TaskDir 是任务的端点目录。taskID 须已校验为路径安全。
func TaskDir(dir, taskID string) string {
	return filepath.Join(dir, taskID)
}

// endpoint 是一个浏览器服务的端点文件内容（service.py 写入）。
type endpoint struct {
	Port int    `json:"port"`
	Key  string `json:"key"`
	PID  int    `json:"pid"`
}

// EndpointPID 返回端点文件里记录的服务进程号。
func EndpointPID(dir, taskID, identity string) (int, bool) {
	raw, err := os.ReadFile(endpointFile(dir, taskID, identity)) // #nosec G304 // 任务与身份由调用方校验为路径安全
	if err != nil {
		return 0, false
	}
	var ep endpoint
	if json.Unmarshal(raw, &ep) != nil || ep.PID <= 0 {
		return 0, false
	}
	return ep.PID, true
}

// RemoveEndpoint 删除任务某身份的端点文件。
func RemoveEndpoint(dir, taskID, identity string) {
	_ = os.Remove(endpointFile(dir, taskID, identity))
}

// SweepTask 清理任务端点目录里不属于运行中服务的端点（live 判定身份的服务是否在运行），
// 目录空了即删除。调用方须保证判定期间不会有新服务登记（持有登记锁）。
func SweepTask(dir, taskID string, live func(identity string) bool) {
	if dir == "" {
		return
	}
	entries, err := os.ReadDir(TaskDir(dir, taskID))
	if err != nil {
		return // 目录不存在：任务没有端点
	}
	for _, e := range entries {
		// <身份>.json 与服务写了一半的 <身份>.json.<进程号>.tmp
		identity, _, _ := strings.Cut(e.Name(), ".json")
		if !live(identity) {
			_ = os.Remove(filepath.Join(TaskDir(dir, taskID), e.Name()))
		}
	}
	_ = os.Remove(TaskDir(dir, taskID)) // 仍有运行中服务的端点时不为空，留着
}

func endpointFile(dir, taskID, identity string) string {
	return filepath.Join(TaskDir(dir, taskID), identity+".json")
}

// ReleaseTask 关闭任务的全部浏览器会话（服务进程收到 reset 后关闭 chromium 并退出），
// 返回成功关闭的会话数。dir 是端点目录；只动本任务目录下的端点，并发任务不受影响。
func ReleaseTask(ctx context.Context, dir, taskID string) (int, error) {
	if dir == "" {
		return 0, nil
	}
	files, err := filepath.Glob(filepath.Join(TaskDir(dir, taskID), "*.json"))
	if err != nil {
		return 0, fmt.Errorf("列出浏览器会话: %w", err)
	}
	closed := 0
	var errs []error
	for _, file := range files {
		live, err := reset(ctx, file)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", filepath.Base(file), err))
			continue
		}
		if live {
			closed++
		}
	}
	return closed, errors.Join(errs...)
}

// reset 向端点文件指向的浏览器服务发送 reset 并等它回包，返回服务是否在运行。服务已不在
// （连不上，或端口已被别的进程占用、口令不符）时删掉残留的端点文件。
func reset(ctx context.Context, file string) (bool, error) {
	raw, err := os.ReadFile(file) // #nosec G304 // 端点目录由沙盒管理
	if err != nil {
		return false, nil //nolint:nilerr // 服务退出时自行删除了端点文件
	}
	var ep endpoint
	if err := json.Unmarshal(raw, &ep); err != nil || ep.Port <= 0 {
		_ = os.Remove(file)
		return false, nil //nolint:nilerr // 写了一半的端点文件：服务未就绪即退出，清掉即可
	}
	ctx, cancel := context.WithTimeout(ctx, resetTimeout)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(ep.Port)))
	if err != nil {
		_ = os.Remove(file)
		return false, nil //nolint:nilerr // 连不上 = 服务已退出，只剩残留端点，清掉即完成释放
	}
	defer func() { _ = conn.Close() }()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	req, _ := json.Marshal(map[string]string{"sub": "reset", "key": ep.Key})
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return true, fmt.Errorf("发送 reset: %w", err)
	}
	body, err := io.ReadAll(conn)
	if err != nil {
		return true, fmt.Errorf("等待回包: %w", err)
	}
	var resp struct {
		Unauthorized bool `json:"unauthorized"`
	}
	if json.Unmarshal(body, &resp) == nil && resp.Unauthorized {
		_ = os.Remove(file)
		return false, nil
	}
	return true, nil
}

func lastLine(out []byte, err error) string {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if s := strings.TrimSpace(lines[len(lines)-1]); s != "" {
		return s
	}
	return err.Error()
}

// shellQuote 用单引号包裹 s，供 sh 原样解析。
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
