package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/sandbox"
)

// fakeSandboxClient 按预设脚本回放 Exec 结果——drive_browser 的 CLI 输出语义测试夹具。
type fakeSandboxClient struct {
	responses []sandbox.ExecResult
	calls     int
	commands  []string // 记录每次执行的命令（env 前缀断言用）
	reqs      []sandbox.ExecRequest
}

func (f *fakeSandboxClient) Exec(_ context.Context, req sandbox.ExecRequest) (sandbox.ExecResult, error) {
	f.commands = append(f.commands, req.Command)
	f.reqs = append(f.reqs, req)
	if f.calls < len(f.responses) {
		r := f.responses[f.calls]
		f.calls++
		return r, nil
	}
	return sandbox.ExecResult{}, nil
}

func runBrowserTool(t *testing.T, client CommandRunner, args string) string {
	t.Helper()
	tool := newDriveBrowserTool(Deps{Sandbox: client}, 5*time.Second)
	res, err := tool.Execute(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("执行失败: %v", err)
	}
	return res.Output
}

// 元素编号失效（页面已变化）时输出必须可行动：指引先 state 重建编号再操作。
// 这是浏览器驱动错误恢复的通用契约，不依赖任何具体站点。
func TestDriveBrowser_StaleElementHint(t *testing.T) {
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
func TestDriveBrowser_NoHintOnSuccess(t *testing.T) {
	fake := &fakeSandboxClient{responses: []sandbox.ExecResult{
		{Stdout: "viewport: 1920x1080\n[0] button 登录\n[exit_code: 0]"},
	}}
	out := runBrowserTool(t, fake, `{"action":"state"}`)
	if strings.Contains(out, "提示") {
		t.Fatalf("成功输出不应附指引, got:\n%s", out)
	}
}

// 退化态自愈：watchdog 签名 → 自动 reset 一次并重试原命令，成功则返回重试结果。

// L1 自愈：退化 → reset → 重试成功。
func TestDriveBrowser_WatchdogSelfHealL1(t *testing.T) {
	fake := &fakeSandboxClient{responses: []sandbox.ExecResult{
		{Stdout: "Error: Event handler drive_browser.browser.watchdog_base.BrowserSession timed out after 30.0s", ExitCode: 1}, // 原命令退化
		{Stdout: "ok"}, // L1 reset
		{Stdout: "url: http://target/\n[exit_code: 0]"}, // 重试成功
	}}
	out := runBrowserTool(t, fake, `{"action":"open","url":"http://target/"}`)
	if !strings.Contains(out, "url: http://target/") {
		t.Fatalf("L1 自愈后应返回重试成功结果, got:\n%s", out)
	}
	if !strings.Contains(out, "L1 reset") {
		t.Fatal("自愈过程应留痕（L1 输出可见，供审计）")
	}
	if strings.Contains(out, "L2") {
		t.Fatal("L1 恢复后不应继续 L2")
	}
	if fake.calls != 3 {
		t.Fatalf("应为 原命令+reset+重试 共 3 次调用, got %d", fake.calls)
	}
}

// L2 自愈：reset 救不回（daemon wedged）→ 杀 daemon 冷启动 → 重试成功。
func TestDriveBrowser_WatchdogSelfHealL2(t *testing.T) {
	fake := &fakeSandboxClient{responses: []sandbox.ExecResult{
		{Stdout: "Error: Event handler drive_browser.browser.watchdog_base timed out", ExitCode: 1}, // 原命令退化
		{Stdout: "ok"}, // L1 reset
		{Stdout: "Error: ...watchdog_base timed out", ExitCode: 1}, // L1 后仍退化
		{Stdout: "ok"}, // L2 kill
		{Stdout: "url: http://target/\n[exit_code: 0]"}, // L2 后重试成功
	}}
	out := runBrowserTool(t, fake, `{"action":"open","url":"http://target/"}`)
	if !strings.Contains(out, "url: http://target/") {
		t.Fatalf("L2 自愈后应返回重试成功结果, got:\n%s", out)
	}
	if !strings.Contains(out, "L2 强制结束本身份的浏览器服务") {
		t.Fatal("L2 升级应留痕")
	}
	if fake.calls != 5 {
		t.Fatalf("应为 原命令+L1两步+L2两步 共 5 次调用, got %d", fake.calls)
	}
}

// 两级都失败：不无限自愈，保留完整诊断。
func TestDriveBrowser_WatchdogSelfHealExhausted(t *testing.T) {
	fake := &fakeSandboxClient{responses: []sandbox.ExecResult{
		{Stdout: "Error: ...watchdog_base... timed out", ExitCode: 1}, // 原命令
		{Stdout: "ok"}, // L1 reset
		{Stdout: "Error: ...watchdog_base... timed out", ExitCode: 1}, // L1 后
		{Stdout: "ok"}, // L2 kill
		{Stdout: "Error: ...watchdog_base... timed out", ExitCode: 1}, // L2 后仍坏
	}}
	out := runBrowserTool(t, fake, `{"action":"state"}`)
	if !strings.Contains(out, "watchdog_base") {
		t.Fatalf("耗尽后应保留原始诊断, got:\n%s", out)
	}
	if fake.calls != 5 {
		t.Fatalf("只到 L2（防循环）, got %d calls", fake.calls)
	}
}

// AGENT_ID 按 action 隔离 tab：并行执行 action 时（engine 已把 actionID 挂入
// ctx），drive_browser 命令必须带 AGENT_ID 前缀，否则多个 action 共抢同一 tab。
func TestDriveBrowser_AgentIDTabIsolation(t *testing.T) {
	fake := &fakeSandboxClient{responses: []sandbox.ExecResult{
		{Stdout: "url: http://t/\n[exit_code: 0]"},
	}}
	tool := newDriveBrowserTool(Deps{Sandbox: fake}, 5*time.Second)
	ctx := WithActionID(context.Background(), "act-123")
	res, err := tool.Execute(ctx, json.RawMessage(`{"action":"open","url":"http://t/"}`))
	if err != nil || res.Error != "" {
		t.Fatalf("执行失败: %v %s", err, res.Error)
	}
	if len(fake.commands) != 1 || !strings.Contains(fake.commands[0], `AGENT_ID="act-123"`) {
		t.Fatalf("命令应带 AGENT_ID 前缀, got %v", fake.commands)
	}

	// 无 action 上下文（如 judge 复核侧）：不带 AGENT_ID（CLI 默认 default tab）
	fake2 := &fakeSandboxClient{responses: []sandbox.ExecResult{{Stdout: "ok"}}}
	tool2 := newDriveBrowserTool(Deps{Sandbox: fake2}, 5*time.Second)
	_, _ = tool2.Execute(context.Background(), json.RawMessage(`{"action":"state"}`))
	if strings.Contains(fake2.commands[0], "AGENT_ID") {
		t.Fatalf("无 action 上下文不应带 AGENT_ID, got %s", fake2.commands[0])
	}
}

// P2：浏览器身份 task 域化——IDENTITY 必为 "{taskID}-{身份名}"（跨任务/跨身份
// 双隔离的 cookie jar 边界），身份名非法字符清洗（要进 socket 文件名）。
func TestDriveBrowser_TaskScopedIdentity(t *testing.T) {
	fake := &fakeSandboxClient{responses: []sandbox.ExecResult{{Stdout: "ok"}}}
	tool := newDriveBrowserTool(Deps{Sandbox: fake, TaskID: "t-42"}, 5*time.Second)
	_, _ = tool.Execute(context.Background(), json.RawMessage(`{"action":"state"}`))
	if !strings.Contains(fake.commands[0], `IDENTITY="t-42-default"`) {
		t.Fatalf("默认身份应为 {taskID}-default, got %s", fake.commands[0])
	}

	fake2 := &fakeSandboxClient{responses: []sandbox.ExecResult{{Stdout: "ok"}}}
	tool2 := newDriveBrowserTool(Deps{Sandbox: fake2, TaskID: "t-42"}, 5*time.Second)
	_, _ = tool2.Execute(context.Background(), json.RawMessage(`{"action":"state","identity":"admin/foo 户"}`))
	if !strings.Contains(fake2.commands[0], `IDENTITY="t-42-admin-foo--"`) {
		t.Fatalf("身份名非法字符应清洗为 -, got %s", fake2.commands[0])
	}

	// 无 taskID（如 judge 复核侧）：裸身份名，不带任务前缀
	fake3 := &fakeSandboxClient{responses: []sandbox.ExecResult{{Stdout: "ok"}}}
	tool3 := newDriveBrowserTool(Deps{Sandbox: fake3}, 5*time.Second)
	_, _ = tool3.Execute(context.Background(), json.RawMessage(`{"action":"state","identity":"admin"}`))
	if !strings.Contains(fake3.commands[0], `IDENTITY="admin"`) {
		t.Fatalf("无 taskID 应为裸身份名, got %s", fake3.commands[0])
	}
}

// P3：清理脚本只匹配本任务前缀的身份 socket。

func TestRunCommandDesc_ListenerContract(t *testing.T) {
	// 未发布监听段：明示回连类操作不可达——LLM 不知道就会绑 4444 白跑一步。
	plain := newRunCommandTool(Deps{}, time.Minute)
	if got := plain.Desc(); !strings.Contains(got, "回连类操作不可达") {
		t.Fatalf("未发布段时应明示回连不可达: %q", got)
	}

	// 已发布段 + 回连地址：必须把「绑哪个段、回连写什么」一次说清。
	tool := newRunCommandTool(Deps{
		ListenerPortStart: 21000,
		ListenerPortCount: 100,
		CallbackHost:      "203.0.113.7",
	}, time.Minute)
	desc := tool.Desc()
	for _, want := range []string{"21000-21099", "203.0.113.7"} {
		if !strings.Contains(desc, want) {
			t.Fatalf("描述应含 %q: %q", want, desc)
		}
	}

	// 已发布段但回连地址未配置：显式告知缺失，防 LLM 编造地址。
	noHost := newRunCommandTool(Deps{ListenerPortStart: 21000, ListenerPortCount: 100}, time.Minute)
	if got := noHost.Desc(); !strings.Contains(got, "callback_host") {
		t.Fatalf("回连地址缺失时应指向 sandbox.callback_host: %q", got)
	}
}

// 自愈的修复与重试请求必须带齐批次/任务参数：sandbox-server 拒绝空 assignment_id，
// 漏传会让整条恢复链必然失败。修复后仍退化时本次调用记为结果性失败。
func TestDriveBrowser_HealRequestsCarryAssignmentAndFailWhenUnrecovered(t *testing.T) {
	degraded := sandbox.ExecResult{Stdout: "watchdog timeout"}
	client := &fakeSandboxClient{responses: []sandbox.ExecResult{degraded, {}, degraded, {}, degraded}}
	tool := newDriveBrowserTool(Deps{Sandbox: client, AssignmentID: "asg-1", TaskID: "task-1"}, 5*time.Second)
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"state"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(client.reqs) != 5 {
		t.Fatalf("应执行 原命令 + 2×(修复+重试) = 5 次，得 %d", len(client.reqs))
	}
	for i, r := range client.reqs {
		if r.AssignmentID != "asg-1" || r.TaskID != "task-1" {
			t.Fatalf("第 %d 次请求缺批次/任务参数: %+v", i, r)
		}
	}
	if res.Error == "" {
		t.Fatal("自愈耗尽应记为结果性失败（ToolResult.Error 非空）")
	}
}

// 自愈只作用于出问题的身份：修复命令带上同一身份，不波及同任务的其他身份、更不波及其他
// 任务的浏览器（不按进程名在全机范围内结束进程）。
func TestDriveBrowser_SelfHealScopedToIdentity(t *testing.T) {
	degraded := sandbox.ExecResult{Stdout: "Error: ...watchdog_base... timed out", ExitCode: 1}
	fake := &fakeSandboxClient{responses: []sandbox.ExecResult{degraded, {Stdout: "ok"}, degraded, {Stdout: "ok"}, degraded}}
	tool := newDriveBrowserTool(Deps{Sandbox: fake, TaskID: "t-42"}, 5*time.Second)
	_, _ = tool.Execute(context.Background(), json.RawMessage(`{"action":"state","identity":"admin"}`))
	if len(fake.commands) != 5 {
		t.Fatalf("应执行 原命令+L1两步+L2两步, got %v", fake.commands)
	}
	for _, i := range []int{1, 3} {
		cmd := fake.commands[i]
		if !strings.Contains(cmd, `IDENTITY="t-42-admin"`) || !strings.Contains(cmd, "browser-use ") {
			t.Errorf("修复命令应经 browser-use 作用于本身份, got %q", cmd)
		}
		if strings.Contains(cmd, "pkill") || strings.Contains(cmd, "killall") {
			t.Errorf("修复命令不得按进程名全机结束进程, got %q", cmd)
		}
	}
}
