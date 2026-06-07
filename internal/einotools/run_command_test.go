package einotools

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/V3teran/liusha/internal/sandbox"
)

type fakeSandbox struct {
	gotReq sandbox.ExecRequest
	result sandbox.ExecResult
}

func (f *fakeSandbox) Exec(_ context.Context, req sandbox.ExecRequest) (sandbox.ExecResult, error) {
	f.gotReq = req
	return f.result, nil
}

// enhancedInvoke 调增强接口拿 *ToolResult。
func enhancedInvoke(t *testing.T, bt tool.BaseTool, argsJSON string) *schema.ToolResult {
	t.Helper()
	et, ok := bt.(tool.EnhancedInvokableTool)
	if !ok {
		t.Fatal("run_command 应实现 EnhancedInvokableTool")
	}
	res, err := et.InvokableRun(context.Background(), &schema.ToolArgument{Text: argsJSON})
	if err != nil {
		t.Fatalf("InvokableRun 报错: %v", err)
	}
	return res
}

func TestRunCommand_TextOnlyWithFileMeta(t *testing.T) {
	sb := &fakeSandbox{result: sandbox.ExecResult{
		ExitCode: 0,
		Stdout:   "back-end DBMS: MySQL",
		Files: []sandbox.Attachment{
			{Name: "shot.png", B64: "aW1hZ2VkYXRh"}, // 截图
			{Name: "dump.txt", B64: "dGV4dA=="},     // 非图片
		},
	}}
	rc, err := BuildRunCommand(sb, "hunter-1", 600, 0)
	if err != nil {
		t.Fatal(err)
	}
	res := enhancedInvoke(t, rc, `{"command":"sqlmap -u x","timeout_seconds":300,"tag":"sqlmap-l5"}`)

	// 只返 1 个 text part（image 不再作 tool-role multimodal part —— mimo 400 修复）
	if len(res.Parts) != 1 {
		t.Fatalf("应只有 1 个 text part（image 不回灌），得到 %d", len(res.Parts))
	}
	p := res.Parts[0]
	if p.Type != schema.ToolPartTypeText || !strings.Contains(p.Text, "back-end DBMS") {
		t.Errorf("part 应是含 stdout 的 text: %+v", p)
	}
	// 文本里仍列出附件（含截图元信息 image=true，让 LLM 知道有截图）
	if !strings.Contains(p.Text, "shot.png") || !strings.Contains(p.Text, "dump.txt") {
		t.Errorf("附件应列在 text files（含截图元信息）: %s", p.Text)
	}
	if !strings.Contains(p.Text, `"image":true`) {
		t.Errorf("截图附件应标 image=true: %s", p.Text)
	}
	// hunterID 闭包注入
	if sb.gotReq.HunterID != "hunter-1" {
		t.Errorf("HunterID 注入错: %q", sb.gotReq.HunterID)
	}
}

func TestRunCommand_ClampsTimeout(t *testing.T) {
	sb := &fakeSandbox{}
	rc, _ := BuildRunCommand(sb, "h1", 600, 0)
	enhancedInvoke(t, rc, `{"command":"x","timeout_seconds":99999,"tag":"t"}`)
	if sb.gotReq.TimeoutSeconds != 600 {
		t.Errorf("timeout 应钳到 600，得到 %d", sb.gotReq.TimeoutSeconds)
	}
}

func TestRunCommand_SanitizesTag(t *testing.T) {
	sb := &fakeSandbox{}
	rc, _ := BuildRunCommand(sb, "h1", 600, 0)
	enhancedInvoke(t, rc, `{"command":"x","timeout_seconds":10,"tag":"bad tag!"}`)
	if sb.gotReq.Tag != "default" {
		t.Errorf("非法 tag 应归 default，得到 %q", sb.gotReq.Tag)
	}
}

func TestRunCommand_CommandRequired(t *testing.T) {
	sb := &fakeSandbox{}
	rc, _ := BuildRunCommand(sb, "h1", 600, 0)
	et := rc.(tool.EnhancedInvokableTool)
	if _, err := et.InvokableRun(context.Background(), &schema.ToolArgument{Text: `{"timeout_seconds":10,"tag":"t"}`}); err == nil {
		t.Fatal("缺 command 应报错")
	}
}

func TestRunCommand_NilExecutorErrors(t *testing.T) {
	if _, err := BuildRunCommand(nil, "h1", 600, 0); err == nil {
		t.Fatal("nil executor 应报错")
	}
	if _, err := BuildRunCommand(&fakeSandbox{}, "", 600, 0); err == nil {
		t.Fatal("空 hunterID 应报错")
	}
}

func TestClampMiddle(t *testing.T) {
	short := "hello"
	if got := clampMiddle(short, 100); got != short {
		t.Errorf("短串应原样返回，得到 %q", got)
	}
	long := strings.Repeat("A", 200) + strings.Repeat("B", 200)
	got := clampMiddle(long, 100)
	if !strings.Contains(got, "截断") || len(got) >= len(long) {
		t.Errorf("长串应被截断带标记: len=%d", len(got))
	}
}

func TestInfo_SchemaHasParams(t *testing.T) {
	rc, _ := BuildRunCommand(&fakeSandbox{}, "h1", 900, 0)
	info, err := rc.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "run_command" {
		t.Errorf("name 错: %s", info.Name)
	}
	js, err := info.ParamsOneOf.ToJSONSchema()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"command", "timeout_seconds", "tag"} {
		if _, ok := js.Properties.Get(p); !ok {
			t.Errorf("schema 缺参数 %s", p)
		}
	}
}
