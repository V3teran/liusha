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
	plain := newRunCommandTool(Deps{StepToolTimeoutSeconds: 1800, RunTailBytes: 8192}, time.Minute)
	if got := plain.Desc(); !strings.Contains(got, "回连的操作不可达") {
		t.Fatalf("未发布段时应明示回连不可达: %q", got)
	}

	// 已发布段 + 回连地址：必须把「绑哪个段、回连写什么」一次说清。
	tool := newRunCommandTool(Deps{
		ListenerPortStart:      21000,
		ListenerPortCount:      100,
		CallbackHost:           "203.0.113.7",
		StepToolTimeoutSeconds: 1800,
		RunTailBytes:           8192,
	}, time.Minute)
	desc := tool.Desc()
	for _, want := range []string{"21000-21099", "203.0.113.7"} {
		if !strings.Contains(desc, want) {
			t.Fatalf("描述应含 %q: %q", want, desc)
		}
	}

	// 已发布段但回连地址未配置：显式告知缺失，防 LLM 编造地址。
	noHost := newRunCommandTool(Deps{ListenerPortStart: 21000, ListenerPortCount: 100, StepToolTimeoutSeconds: 1800, RunTailBytes: 8192}, time.Minute)
	if got := noHost.Desc(); !strings.Contains(got, "callback_host") {
		t.Fatalf("回连地址缺失时应指向 sandbox.callback_host: %q", got)
	}
}

// 装配处必须给出 runtime 配置的值：工具不内置出厂值的第二份——配置改了它不会跟着变，
// 留着只会让人以为「没配也行」。
func TestNewRunCommandTool_RequiresConfiguredLimits(t *testing.T) {
	ok := Deps{StepToolTimeoutSeconds: 1800, RunTailBytes: 8192}
	for _, bad := range []Deps{
		{},                             // 两者都缺
		{StepToolTimeoutSeconds: 1800}, // 缺尾部保留
		{RunTailBytes: 8192},           // 缺超时上限
		{StepToolTimeoutSeconds: -5, RunTailBytes: 8192},
		{StepToolTimeoutSeconds: 1800, RunTailBytes: 0},
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
	newRunCommandTool(Deps{StepToolTimeoutSeconds: 1800, RunTailBytes: 8192}, 0)
}

// 输出尾部保留 = runtime.run_tail_bytes，没有第二份：配置 100 就截到 100（写死的 8192 兜底
// 已删——9000 字节的输入若落到 8192 兜底会变成截断）；n<=0 原样返回——保守失败，不悄悄换成
// 一个写死的数（输入取 9000 字节：比任何写死的兜底都长，兜底一复活这里就不再原样）。
func TestTailRunOutput_UsesConfiguredLimit(t *testing.T) {
	long := strings.Repeat("a", 9000)
	got := tailRunOutput(long, 100)
	if !strings.Contains(got, "前段已截断") || !strings.HasSuffix(got, strings.Repeat("a", 100)) || len(got) >= len(long) {
		t.Fatalf("应按配置保留尾部 100 字节（加截断标记），实际 %d 字节", len(got))
	}
	if got := tailRunOutput(long, 0); got != long {
		t.Fatalf("n<=0 应原样返回（不截断、不换默认值），实际 %d 字节", len(got))
	}
}

// capturingSandbox 记下收到的执行请求（Execute 钳制的观察点）。
type capturingSandbox struct{ got sandbox.ExecRequest }

func (s *capturingSandbox) Exec(_ context.Context, req sandbox.ExecRequest) (sandbox.ExecResult, error) {
	s.got = req
	return sandbox.ExecResult{Stdout: "done"}, nil
}

// Execute 的超时钳制上界 = runtime.step_tool_timeout_seconds：模型自传更长的会被截到上界，
// 没自传的用命令默认再被钳到上界。钳制一丢，沙盒就会收到模型要的任意时长。
func TestRunCommandExecute_ClampsToConfiguredCeiling(t *testing.T) {
	sb := &capturingSandbox{}
	tool := newRunCommandTool(Deps{Sandbox: sb, StepToolTimeoutSeconds: 2, RunTailBytes: 8192}, 2*time.Second)
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
