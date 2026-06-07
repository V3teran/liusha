package einoagent_test

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/V3teran/liusha/internal/einoagent"
)

// fakeModel 立即返回无 tool call 的 assistant 消息 → ChatModelAgent 自然收尾。
type fakeModel struct{ reply string }

func (f *fakeModel) Generate(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	return schema.AssistantMessage(f.reply, nil), nil
}
func (f *fakeModel) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, nil
}
func (f *fakeModel) WithTools(_ []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return f, nil
}

type fakeStrikerFactory struct {
	m        model.ToolCallingChatModel
	calls    int
	gotRoles []string
}

func (f *fakeStrikerFactory) For(_ context.Context, role string) (model.ToolCallingChatModel, error) {
	f.calls++
	f.gotRoles = append(f.gotRoles, role)
	return f.m, nil
}

func TestSpawnStriker_BuildsRunsAndReports(t *testing.T) {
	f := allFake{}
	fac := &fakeStrikerFactory{m: &fakeModel{reply: "striker 报告：发现 BAC"}}
	var gotBrief, gotSid string
	cfg := einoagent.StrikerSpawnConfig{
		Factory:     fac,
		ToolDeps:    einoagent.TrackerToolDeps{Findings: f, Notes: f, Lessons: f, Credentials: f},
		Instruction: "you are striker",
		OwnerType:   "active_scan", OwnerID: "o", Host: "h",
		NewHunterID: func() string { return "striker-1" },
		BuildUserPrompt: func(_ context.Context, brief, sid string, _ int64) string {
			gotBrief, gotSid = brief, sid
			return "PROMPT: " + brief
		},
	}
	st, err := einoagent.BuildSpawnStriker(cfg)
	if err != nil {
		t.Fatal(err)
	}
	it := st.(tool.InvokableTool)
	out, err := it.InvokableRun(context.Background(), `{"brief":"挖 /admin 的越权"}`)
	if err != nil {
		t.Fatalf("spawn_striker 报错: %v", err)
	}
	if !strings.Contains(out, "striker-1") {
		t.Errorf("应返回 striker_id: %s", out)
	}
	// 用 striker role 取了独立 model（铁律）
	if fac.calls != 1 || len(fac.gotRoles) != 1 || fac.gotRoles[0] != "striker" {
		t.Errorf("应用 role=striker 取 1 次独立 model: calls=%d roles=%v", fac.calls, fac.gotRoles)
	}
	// BuildUserPrompt 收到 brief + striker id
	if gotBrief != "挖 /admin 的越权" || gotSid != "striker-1" {
		t.Errorf("BuildUserPrompt 入参错: brief=%q sid=%q", gotBrief, gotSid)
	}
}

func TestSpawnStriker_BriefRequired(t *testing.T) {
	fac := &fakeStrikerFactory{m: &fakeModel{}}
	st, _ := einoagent.BuildSpawnStriker(einoagent.StrikerSpawnConfig{
		Factory: fac, NewHunterID: func() string { return "s" },
	})
	it := st.(tool.InvokableTool)
	if _, err := it.InvokableRun(context.Background(), `{}`); err == nil {
		t.Fatal("缺 brief 应报错")
	}
}

func TestBuildSpawnStriker_RequiresFactory(t *testing.T) {
	if _, err := einoagent.BuildSpawnStriker(einoagent.StrikerSpawnConfig{NewHunterID: func() string { return "s" }}); err == nil {
		t.Fatal("缺 Factory 应报错")
	}
}
