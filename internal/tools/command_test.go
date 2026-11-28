package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/sandbox"
)

func TestRunCommandDesc_ListenerContract(t *testing.T) {
	// 未发布监听段：明示回连类操作不可达——LLM 不知道就会绑 4444 白跑一步。
	plain := newRunCommandTool(Deps{StepToolTimeoutSeconds: 1800, OutputWindowBytes: 65536}, time.Minute)
	if got := plain.Desc(); !strings.Contains(got, "回连的操作不可达") {
		t.Fatalf("未发布段时应明示回连不可达: %q", got)
	}

	// 已发布段 + 回连地址：必须把「绑哪个段、回连写什么」一次说清。
	tool := newRunCommandTool(Deps{
		ListenerPortStart:      21000,
		ListenerPortCount:      100,
		CallbackHost:           "203.0.113.7",
		StepToolTimeoutSeconds: 1800,
		OutputWindowBytes:      65536,
	}, time.Minute)
	desc := tool.Desc()
	for _, want := range []string{"21000-21099", "203.0.113.7"} {
		if !strings.Contains(desc, want) {
			t.Fatalf("描述应含 %q: %q", want, desc)
		}
	}

	// 已发布段但回连地址未配置：显式告知缺失，防 LLM 编造地址。
	noHost := newRunCommandTool(Deps{ListenerPortStart: 21000, ListenerPortCount: 100, StepToolTimeoutSeconds: 1800, OutputWindowBytes: 65536}, time.Minute)
	if got := noHost.Desc(); !strings.Contains(got, "callback_host") {
		t.Fatalf("回连地址缺失时应指向 sandbox.callback_host: %q", got)
	}
}

// 装配处必须给出 runtime 配置的值：工具不内置出厂值的第二份——配置改了它不会跟着变，
// 留着只会让人以为「没配也行」。
func TestNewRunCommandTool_RequiresConfiguredLimits(t *testing.T) {
	ok := Deps{StepToolTimeoutSeconds: 1800, OutputWindowBytes: 65536}
	for _, bad := range []Deps{
		{},                             // 两者都缺
		{StepToolTimeoutSeconds: 1800}, // 缺工具结果窗口
		{OutputWindowBytes: 65536},     // 缺超时上限
		{StepToolTimeoutSeconds: -5, OutputWindowBytes: 65536},
		{StepToolTimeoutSeconds: 1800, OutputWindowBytes: 0},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatalf("Deps %+v 缺配置值，构造应报错", bad)
				}
			}()
			newRunCommandTool(bad, time.Minute)
		}()
	}
	newRunCommandTool(ok, time.Minute) // 齐备时照常构造
}

// 注册表级超时 = runtime.step_tool_timeout_seconds，没有第二份：上限写多少就是多少。
func TestRunCommandTimeout_FromConfig(t *testing.T) {
	if got := runCommandTimeout(Deps{StepToolTimeoutSeconds: 600}); got != 10*time.Minute {
		t.Fatalf("注册表级超时应为配置的 600 秒，实际 %v", got)
	}
	// 校验在构造函数：这里只是换算，零值给出零值，不回落到任何写死的数。
	if got := runCommandTimeout(Deps{}); got != 0 {
		t.Fatalf("未给出上限时应得 0（由构造函数拒绝），实际 %v", got)
	}
	// 构造时的超时参数同样须为正。
	defer func() {
		if recover() == nil {
			t.Fatal("注册表级超时非正时构造应报错")
		}
	}()
	newRunCommandTool(Deps{StepToolTimeoutSeconds: 1800, OutputWindowBytes: 65536}, 0)
}

// 命令工具不裁剪输出：沙盒返回多少就交出多少——裁剪只在工具结果进上下文的那一道窗口做。此前这里
// 先只留尾部 8 KiB，扫描类工具的结果在模型看到之前就先被扔掉了前段。落盘阈值取工具结果的窗口，
// 交给沙盒：输出超过窗口时窗口会省掉中间，省掉的部分得有处可读。
func TestRunCommandExecute_ReturnsOutputUncut(t *testing.T) {
	sb := &scriptedSandbox{res: sandbox.ExecResult{Stdout: strings.Repeat("a", 9000), Stderr: "warn", StdoutBytes: 9000, StderrBytes: 4}}
	tool := newRunCommandTool(Deps{Sandbox: sb, StepToolTimeoutSeconds: 60, OutputWindowBytes: 4096}, time.Minute)

	res, err := tool.Execute(context.Background(), json.RawMessage(`{"command":"cat big"}`))
	if err != nil {
		t.Fatal(err)
	}
	if sb.got.SaveOutputOverBytes != 4096 {
		t.Fatalf("落盘阈值应取工具结果的窗口，沙盒收到 %d", sb.got.SaveOutputOverBytes)
	}
	want := strings.Repeat("a", 9000) + "\n--- stderr ---\nwarn\n[exit_code: 0]"
	if res.Output != want {
		t.Fatalf("输出应原样交出（%d 字节），实际 %d 字节: %.80q…", len(want), len(res.Output), res.Output)
	}
}

// 沙盒报告完整输出已落盘的流，结果末尾写明总字节数与文件路径；沙盒的提示（如产物超过数量上限被
// 跳过）也带到结果里，不再静默丢掉。这几行都在末尾：无论窗口怎么切，末尾都保留。
func TestRunCommandExecute_PointsToSavedFullOutput(t *testing.T) {
	sb := &scriptedSandbox{res: sandbox.ExecResult{
		Stdout: "tail", StdoutBytes: 183420, StdoutFile: "/ws/a/t/console/20261127-155900-1.stdout",
		Stderr: "err\n", StderrBytes: 4, StderrFile: "/ws/a/t/console/20261127-155900-1.stderr",
		ExitCode: 1, TimedOut: true,
		Warnings: []string{"file 'x' skipped (count limit reached)"},
		Files:    []sandbox.Attachment{{Name: "a.txt", B64: "AAAA"}},
	}}
	tool := newRunCommandTool(Deps{Sandbox: sb, StepToolTimeoutSeconds: 60, OutputWindowBytes: 4096}, time.Minute)

	res, err := tool.Execute(context.Background(), json.RawMessage(`{"command":"nmap -p- target","timeout_seconds":60}`))
	if err != nil {
		t.Fatal(err)
	}
	want := "tail\n--- stderr ---\nerr\n" +
		"\n[超时: 60s]\n[exit_code: 1]\n" +
		"[stdout 共 183420 字节；完整输出: /ws/a/t/console/20261127-155900-1.stdout]\n" +
		"[stderr 共 4 字节；完整输出: /ws/a/t/console/20261127-155900-1.stderr]\n" +
		"[沙盒提示: file 'x' skipped (count limit reached)]\n" +
		"[out/ 产物 1 个: a.txt(0KB) —— 已存档，操作员可在工作空间页查看]"
	if res.Output != want {
		t.Fatalf("结果不符:\n.want %q\n.got  %q", want, res.Output)
	}
}

// 工具描述写明长输出只见头尾、缺的在文件里：模型据此分段读文件，而不是重跑命令。
func TestRunCommandDesc_ExplainsLongOutput(t *testing.T) {
	desc := newRunCommandTool(Deps{StepToolTimeoutSeconds: 1800, OutputWindowBytes: 65536}, time.Minute).Desc()
	for _, want := range []string{"开头和结尾", "文件路径", "不要重跑命令"} {
		if !strings.Contains(desc, want) {
			t.Fatalf("描述应含 %q: %q", want, desc)
		}
	}
}

// scriptedSandbox 按预设回应执行，记下收到的请求。
type scriptedSandbox struct {
	got sandbox.ExecRequest
	res sandbox.ExecResult
}

func (s *scriptedSandbox) Exec(_ context.Context, req sandbox.ExecRequest) (sandbox.ExecResult, error) {
	s.got = req
	return s.res, nil
}

// Execute 的超时钳制上界 = runtime.step_tool_timeout_seconds：模型自传更长的会被截到上界，
// 没自传的用命令默认再被钳到上界。钳制一丢，沙盒就会收到模型要的任意时长。
func TestRunCommandExecute_ClampsToConfiguredCeiling(t *testing.T) {
	sb := &scriptedSandbox{}
	tool := newRunCommandTool(Deps{Sandbox: sb, StepToolTimeoutSeconds: 2, OutputWindowBytes: 65536}, 2*time.Second)
	ctx := context.Background()

	if _, err := tool.Execute(ctx, json.RawMessage(`{"command":"echo hi","timeout_seconds":100}`)); err != nil {
		t.Fatal(err)
	}
	if sb.got.TimeoutSeconds != 2 {
		t.Fatalf("自传 100 秒应被钳到配置的上限 2 秒，沙盒收到 %d 秒", sb.got.TimeoutSeconds)
	}
	if _, err := tool.Execute(ctx, json.RawMessage(`{"command":"echo hi"}`)); err != nil {
		t.Fatal(err)
	}
	if sb.got.TimeoutSeconds != 2 {
		t.Fatalf("未自传超时应取默认再被钳到 2 秒，沙盒收到 %d 秒", sb.got.TimeoutSeconds)
	}
}
