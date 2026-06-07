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

func TestRunCommand_TextAndImageParts(t *testing.T) {
	sb := &fakeSandbox{result: sandbox.ExecResult{
		ExitCode: 0,
		Stdout:   "back-end DBMS: MySQL",
		Files: []sandbox.Attachment{
			{Name: "shot.png", B64: "aW1hZ2VkYXRh"}, // "imagedata"
			{Name: "dump.txt", B64: "dGV4dA=="},     // 非图片
		},
	}}
	rc, err := BuildRunCommand(sb, "hunter-1", 600, 0)
	if err != nil {
		t.Fatal(err)
	}
	res := enhancedInvoke(t, rc, `{"command":"sqlmap -u x","timeout_seconds":300,"tag":"sqlmap-l5"}`)

	if len(res.Parts) != 2 {
		t.Fatalf("应有 2 个 part（text + 1 image），得到 %d", len(res.Parts))
	}
	// part0 text 含 stdout 关键词 + 非图片文件元信息
	if res.Parts[0].Type != schema.ToolPartTypeText || !strings.Contains(res.Parts[0].Text, "back-end DBMS") {
		t.Errorf("part0 应是含 stdout 的 text: %+v", res.Parts[0])
	}
	if !strings.Contains(res.Parts[0].Text, "dump.txt") {
		t.Errorf("非图片附件应列在 text files: %s", res.Parts[0].Text)
	}
	// part1 image 带 base64 + mime
	img := res.Parts[1]
	if img.Type != schema.ToolPartTypeImage || img.Image == nil {
		t.Fatalf("part1 应是 image: %+v", img)
	}
	if img.Image.MIMEType != "image/png" || img.Image.Base64Data == nil || *img.Image.Base64Data != "aW1hZ2VkYXRh" {
		t.Errorf("image part base64/mime 错: %+v", img.Image)
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
