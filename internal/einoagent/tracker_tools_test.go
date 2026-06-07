package einoagent_test

import (
	"context"
	"encoding/json"
	"sort"
	"testing"

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

func toolNames(t *testing.T, deps einoagent.TrackerToolDeps) []string {
	t.Helper()
	tools, err := einoagent.BuildTrackerTools(deps, einoagent.TrackerToolParams{
		OwnerType: "passive_session", OwnerID: "o1", HunterID: "h1", Host: "host1", FlowID: 3,
	})
	if err != nil {
		t.Fatalf("BuildTrackerTools: %v", err)
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

func TestBuildTrackerTools_Mandatory9(t *testing.T) {
	f := allFake{}
	names := toolNames(t, einoagent.TrackerToolDeps{
		Findings: f, Notes: f, Lessons: f, Credentials: f,
	})
	want := []string{
		"read_credentials", "read_findings", "read_lessons", "read_notes",
		"update_finding", "write_credential", "write_finding", "write_lesson", "write_note",
	}
	if len(names) != len(want) {
		t.Fatalf("必装应 9 个，得到 %d: %v", len(names), names)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("工具名不符 [%d]: 得 %q want %q（全集 %v）", i, names[i], want[i], names)
		}
	}
}

func TestBuildCommanderTools_HasSpawnNoWriteFinding(t *testing.T) {
	f := allFake{}
	// 真 spawn 工具（fake factory，不会真跑）
	spawn, err := einoagent.BuildSpawnStriker(einoagent.StrikerSpawnConfig{
		Factory:     &fakeStrikerFactory{m: &fakeModel{}},
		NewHunterID: func(context.Context) (string, error) { return "s", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	tools, err := einoagent.BuildCommanderTools(einoagent.TrackerToolDeps{
		Findings: f, Notes: f, Lessons: f, Credentials: f, Flows: fakeFlowStore{},
	}, einoagent.TrackerToolParams{OwnerType: "active_scan", OwnerID: "o", HunterID: "cmd", Host: "host"}, spawn)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, bt := range tools {
		info, _ := bt.Info(context.Background())
		names[info.Name] = true
	}
	// 必须有 spawn_striker + list/view_flow + read_findings
	for _, want := range []string{"spawn_striker", "list_flows", "view_flow", "read_findings"} {
		if !names[want] {
			t.Errorf("commander 应含 %s，实际 %v", want, names)
		}
	}
	// 铁律：commander 绝不注册 write_finding / update_finding（硬阻断自挖）
	for _, banned := range []string{"write_finding", "update_finding"} {
		if names[banned] {
			t.Errorf("commander 铁律：不应注册 %s（应转 spawn_striker）", banned)
		}
	}
}

func TestBuildStrikerTools_AddsListAndViewFlow(t *testing.T) {
	f := allFake{}
	tools, err := einoagent.BuildStrikerTools(einoagent.TrackerToolDeps{
		Findings: f, Notes: f, Lessons: f, Credentials: f,
		Flows: fakeFlowStore{}, // 触发 replay + list + view_flow
	}, einoagent.TrackerToolParams{OwnerType: "active_scan", OwnerID: "o", HunterID: "h", Host: "host"})
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, bt := range tools {
		info, _ := bt.Info(context.Background())
		names[info.Name] = true
	}
	for _, want := range []string{"list_flows", "view_flow", "replay_flow", "write_finding"} {
		if !names[want] {
			t.Errorf("striker 工具集应含 %s，实际 %v", want, names)
		}
	}
}

func TestBuildTrackerTools_SandboxAddsRunCommand(t *testing.T) {
	f := allFake{}
	names := toolNames(t, einoagent.TrackerToolDeps{
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
	if len(names) != 10 {
		t.Errorf("9 必装 + run_command = 10，得到 %d: %v", len(names), names)
	}
}
