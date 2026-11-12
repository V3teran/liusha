// Package browser 是沙盒的浏览器能力：浏览器服务脚本（client.py / service.py）编进程序，
// 沙盒启动时释放到本地目录；按任务关闭浏览器会话；探测浏览器能力是否可用。
//
// 每个浏览器身份（{任务ID}-{身份名}）一个常驻服务进程，经 unix 套接字
// /tmp/browser-svc-<身份>.sock 接收命令（约定与 client.py 一致）。drive_browser 工具在沙盒里执行
// `browser-use <子命令>`，由本包生成的包装脚本以 sandbox.browser_python 运行 client.py。
package browser

import (
	"bufio"
	"context"
	"embed"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

//go:embed client.py service.py
var scripts embed.FS

// socketDir 是浏览器服务套接字所在目录（与 client.py 的 SOCK 约定一致；测试可替换）。
var socketDir = "/tmp"

// Install 把浏览器脚本释放到 dir，并在 dir/bin 生成以 python 运行客户端的 browser-use 包装脚本。
// 返回应加到命令 PATH 最前面的目录。每次启动覆盖写入，脚本随程序版本更新。
func Install(dir, python string) (string, error) {
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o750); err != nil {
		return "", fmt.Errorf("创建浏览器脚本目录: %w", err)
	}
	for _, name := range []string{"client.py", "service.py"} {
		b, err := scripts.ReadFile(name)
		if err != nil {
			return "", fmt.Errorf("读取内置脚本 %s: %w", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o640); err != nil { // #nosec G306 // 只读脚本，解释器读取即可
			return "", fmt.Errorf("释放 %s: %w", name, err)
		}
	}
	wrapper := fmt.Sprintf("#!/bin/sh\nexec %s %s \"$@\"\n", shellQuote(python), shellQuote(filepath.Join(dir, "client.py")))
	if err := os.WriteFile(filepath.Join(binDir, "browser-use"), []byte(wrapper), 0o750); err != nil { // #nosec G306 // 包装脚本须可执行
		return "", fmt.Errorf("生成 browser-use 包装脚本: %w", err)
	}
	return binDir, nil
}

// Status 是浏览器能力的探测结果。
type Status struct {
	Installed bool
	Detail    string // 未安装时说明缺什么
}

// probeTimeout 是一次能力探测的上限（导入 browser_use 依赖较多，冷启动要几秒）。
const probeTimeout = 30 * time.Second

// Probe 探测浏览器能力：python 能导入浏览器服务依赖的库即视为可用（chromium 由 browser_use
// 自行定位或安装，无法在不启动浏览器的前提下可靠探测）。
func Probe(ctx context.Context, python string) Status {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	if _, err := exec.LookPath(python); err != nil {
		return Status{Detail: fmt.Sprintf("找不到 Python 解释器 %q（配置 sandbox.browser_python）", python)}
	}
	out, err := exec.CommandContext(ctx, python, "-c", "import browser_use, httpx").CombinedOutput() // #nosec G204 // 解释器来自部署配置
	if err != nil {
		return Status{Detail: fmt.Sprintf("%s 无法导入 browser_use: %s", python, lastLine(out, err))}
	}
	return Status{Installed: true}
}

// resetTimeout 是关闭单个浏览器会话的等待上限（关闭 chromium 通常在秒级）。
const resetTimeout = 15 * time.Second

// ReleaseTask 关闭任务的全部浏览器会话（服务进程收到 reset 后关闭 chromium 并退出），
// 返回成功关闭的会话数。只匹配本任务的身份前缀，并发任务不受影响。
func ReleaseTask(ctx context.Context, taskID string) (int, error) {
	socks, err := filepath.Glob(filepath.Join(socketDir, "browser-svc-"+taskID+"-*.sock"))
	if err != nil {
		return 0, fmt.Errorf("列出浏览器会话: %w", err)
	}
	closed := 0
	var errs []error
	for _, sock := range socks {
		live, err := reset(ctx, sock)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", filepath.Base(sock), err))
			continue
		}
		if live {
			closed++
		}
	}
	return closed, errors.Join(errs...)
}

// reset 向一个浏览器服务发送 reset 并等它回包，返回服务是否在运行。服务已不在（残留
// 套接字）时删掉套接字文件。
func reset(ctx context.Context, sock string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, resetTimeout)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", sock)
	if err != nil {
		_ = os.Remove(sock)
		return false, nil //nolint:nilerr // 连不上 = 服务已退出，只剩残留套接字，清掉即完成释放
	}
	defer func() { _ = conn.Close() }()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if _, err := conn.Write([]byte(`{"sub":"reset"}` + "\n")); err != nil {
		return true, fmt.Errorf("发送 reset: %w", err)
	}
	if _, err := bufio.NewReader(conn).ReadString('}'); err != nil {
		return true, fmt.Errorf("等待回包: %w", err)
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
