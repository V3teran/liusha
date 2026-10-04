package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/sandbox"
)

// fakeSandboxClient 按预设脚本回放 Exec 结果——browser_use 的 CLI 输出语义测试夹具。
type fakeSandboxClient struct {
	responses []sandbox.ExecResult
	calls     int
}

func (f *fakeSandboxClient) Exec(_ context.Context, _ sandbox.ExecRequest) (sandbox.ExecResult, error) {
	if f.calls < len(f.responses) {
		r := f.responses[f.calls]
		f.calls++
		return r, nil
	}
	return sandbox.ExecResult{}, nil
}
func (f *fakeSandboxClient) Close() error { return nil }

func runBrowserTool(t *testing.T, client sandbox.Client, args string) string {
	t.Helper()
	tool := newBrowserUseTool(Deps{Sandbox: client}, 5*time.Second, false)
	res, err := tool.Execute(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("执行失败: %v", err)
	}
	return res.Output
}

// 元素编号失效（页面已变化）时输出必须可行动：指引先 state 重建编号再操作。
// 这是浏览器驱动错误恢复的通用契约，不依赖任何具体站点。
func TestBrowserUse_StaleElementHint(t *testing.T) {
	fake := &fakeSandboxClient{responses: []sandbox.ExecResult{
		{Stdout: "error: Element index 3 not found - page may have changed\n[exit_code: 0]"},
	}}
	out := runBrowserTool(t, fake, `{"action":"click","index":3}`)
	if !strings.Contains(out, `"action":"state"`) {
		t.Fatalf("失效错误应附 state 重建编号的可行动指引, got:\n%s", out)
	}
	if !strings.Contains(out, "Element index 3 not found") {
		t.Fatal("原始错误信息应保留（诊断上下文）")
	}
}

// 正常输出不附加指引（避免噪音稀释）。
func TestBrowserUse_NoHintOnSuccess(t *testing.T) {
	fake := &fakeSandboxClient{responses: []sandbox.ExecResult{
		{Stdout: "viewport: 1920x1080\n[0] button 登录\n[exit_code: 0]"},
	}}
	out := runBrowserTool(t, fake, `{"action":"state"}`)
	if strings.Contains(out, "提示") {
		t.Fatalf("成功输出不应附指引, got:\n%s", out)
	}
}

// 退化态自愈：watchdog 签名 → 自动 reset 一次并重试原命令，成功则返回重试结果。
func TestBrowserUse_WatchdogSelfHeal(t *testing.T) {
	fake := &fakeSandboxClient{responses: []sandbox.ExecResult{
		{Stdout: "Error: Event handler browser_use.browser.watchdog_base.BrowserSession timed out after 30.0s", ExitCode: 1}, // 原命令退化
		{Stdout: "ok"}, // reset
		{Stdout: "url: http://target/\n[exit_code: 0]"}, // 重试成功
	}}
	out := runBrowserTool(t, fake, `{"action":"open","url":"http://target/"}`)
	if !strings.Contains(out, "url: http://target/") {
		t.Fatalf("自愈后应返回重试成功结果, got:\n%s", out)
	}
	if !strings.Contains(out, "[自愈]") || !strings.Contains(out, "--- reset 输出 ---") {
		t.Fatal("自愈过程应留痕（reset 输出可见，供审计）")
	}
	if fake.calls != 3 {
		t.Fatalf("应为 原命令+reset+重试 共 3 次调用, got %d", fake.calls)
	}
}

// 重试仍退化：原样返回（不无限自愈），保留诊断上下文。
func TestBrowserUse_WatchdogSelfHealStillDegraded(t *testing.T) {
	fake := &fakeSandboxClient{responses: []sandbox.ExecResult{
		{Stdout: "Error: ...watchdog_base... timed out", ExitCode: 1},
		{Stdout: "ok"},
		{Stdout: "Error: ...watchdog_base... timed out", ExitCode: 1}, // 重试仍坏
	}}
	out := runBrowserTool(t, fake, `{"action":"state"}`)
	if !strings.Contains(out, "watchdog_base") {
		t.Fatalf("重试仍退化应保留原始诊断, got:\n%s", out)
	}
	if fake.calls != 3 {
		t.Fatalf("只自愈一次（防循环）, got %d calls", fake.calls)
	}
}
