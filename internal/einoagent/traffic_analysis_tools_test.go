package einoagent_test

import (
	"context"
	"encoding/json"
	"sort"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/einoagent"
	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/flow"
	"github.com/V3teran/liusha/internal/lesson"
	"github.com/V3teran/liusha/internal/sandbox"
)

// allFake 一把实现 FindingStore + notes.Store + LessonStore + CredentialStore。
type allFake struct{}

func (allFake) ListByOwnerAndHost(context.Context, string, string, string, int) ([]finding.VulnFinding, error) {
	return nil, nil
}
func (allFake) Save(_ context.Context, f finding.VulnFinding) (finding.VulnFinding, error) {
	return f, nil
}
func (allFake) Update(context.Context, string, string, string, json.RawMessage, json.RawMessage) error {
	return nil
}
func (allFake) ReadNotes(context.Context, string, string) ([]byte, error)        { return nil, nil }
func (allFake) AppendNote(context.Context, string, string, []byte) error         { return nil }
func (allFake) ListByHost(context.Context, string, int) ([]lesson.Lesson, error) { return nil, nil }
func (allFake) Add(_ context.Context, l lesson.Lesson) (lesson.Lesson, error)    { return l, nil }
func (allFake) GetIdentitiesByHost(context.Context, string) ([]credential.Identity, error) {
	return nil, nil
}
func (allFake) BatchSave(context.Context, map[string][]credential.Identity, int) error { return nil }

// fakeFlowStore 实现 einoagent.FlowStore（FlowReader + FlowLister）。
type fakeFlowStore struct{}

func (fakeFlowStore) GetByID(context.Context, int64) (flow.Flow, error) { return flow.Flow{}, nil }
func (fakeFlowStore) ListByOwnerFiltered(context.Context, string, flow.ListFilter) ([]flow.FlowSummary, error) {
	return nil, nil
}

// fakeSandboxClient 实现 sandbox.Client。
type fakeSandboxClient struct{}

func (fakeSandboxClient) Exec(context.Context, sandbox.ExecRequest) (sandbox.ExecResult, error) {
	return sandbox.ExecResult{}, nil
}
func (fakeSandboxClient) Close() error { return nil }

func toolNames(t *testing.T, deps einoagent.TrafficAnalysisToolDeps) []string {
	t.Helper()
	tools, err := einoagent.BuildTrafficAnalysisTools(deps, einoagent.TrafficAnalysisToolParams{
		OwnerType: "passive_session", OwnerID: "o1", HunterID: "h1", Host: "host1", FlowID: 3,
	})
	if err != nil {
		t.Fatalf("BuildTrafficAnalysisTools: %v", err)
	}
	var names []string
	for _, bt := range tools {
		info, err := bt.Info(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, info.Name)
	}
	sort.Strings(names)
	return names
}

func TestBuildTrafficAnalysisTools_Mandatory(t *testing.T) {
	f := allFake{}
	names := toolNames(t, einoagent.TrafficAnalysisToolDeps{
		Findings: f, Notes: f, Lessons: f, Credentials: f,
	})
	want := []string{
		"done", "read_credentials", "read_findings", "read_lessons", "read_notes",
		"update_finding", "write_credential", "write_finding", "write_lesson", "write_note",
	}
	if len(names) != len(want) {
		t.Fatalf("必装应 %d 个，得到 %d: %v", len(want), len(names), names)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("工具名不符 [%d]: 得 %q want %q（全集 %v）", i, names[i], want[i], names)
		}
	}
}

// fakeModel 立即返回无 tool call 的 assistant 消息 → ChatModelAgent 自然收尾。
// （从已删的 spawn_test.go 迁来；deep_swarm_test.go 等共用此 fixture。）
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

func TestBuildTrafficAnalysisTools_SandboxAddsRunCommand(t *testing.T) {
	f := allFake{}
	names := toolNames(t, einoagent.TrafficAnalysisToolDeps{
		Findings: f, Notes: f, Lessons: f, Credentials: f,
		Sandbox: fakeSandboxClient{}, MaxTimeoutSeconds: 600,
	})
	found := false
	for _, n := range names {
		if n == "run_command" {
			found = true
		}
	}
	if !found {
		t.Fatalf("注入 Sandbox 后应有 run_command，得到 %v", names)
	}
	if len(names) != 11 {
		t.Errorf("10 必装（含 done）+ run_command = 11，得到 %d: %v", len(names), names)
	}
}
