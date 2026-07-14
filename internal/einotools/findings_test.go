package einotools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"

	"github.com/V3teran/liusha/internal/finding"
)

// fakeStore 同时满足 FindingReader + FindingWriter + FindingUpdater，记录收到的写入用于断言注入字段。
type fakeStore struct {
	list    []finding.VulnFinding
	saved   finding.VulnFinding
	updated struct {
		id, summary, severity string
		target, evidence      json.RawMessage
		dependsOn             []string
	}
}

func (f *fakeStore) ListByTaskAndHost(_ context.Context, _, _ string, _ int) ([]finding.VulnFinding, error) {
	return f.list, nil
}
func (f *fakeStore) Save(_ context.Context, v finding.VulnFinding) (finding.VulnFinding, error) {
	v.ID = "fk-1"
	f.saved = v
	return v, nil
}
func (f *fakeStore) Update(_ context.Context, id, summary, severity string, target, evidence json.RawMessage, dependsOn []string) error {
	f.updated.id = id
	f.updated.summary = summary
	f.updated.severity = severity
	f.updated.target = target
	f.updated.evidence = evidence
	f.updated.dependsOn = dependsOn
	return nil
}

func invoke(t *testing.T, bt tool.BaseTool, argsJSON string) string {
	t.Helper()
	it, ok := bt.(tool.InvokableTool)
	if !ok {
		t.Fatal("工具不是 InvokableTool")
	}
	out, err := it.InvokableRun(context.Background(), argsJSON)
	if err != nil {
		t.Fatalf("InvokableRun 报错: %v", err)
	}
	return out
}

func TestReadFindings(t *testing.T) {
	store := &fakeStore{list: []finding.VulnFinding{
		{ID: "f1", Severity: "high", Summary: "SQLi in /user"},
	}}
	rf, err := BuildReadFindings(store, "task-1", "host-1")
	if err != nil {
		t.Fatal(err)
	}
	out := invoke(t, rf, "{}")
	if !strings.Contains(out, "f1") || !strings.Contains(out, "SQLi in /user") {
		t.Fatalf("read_findings 输出缺 finding: %s", out)
	}
}

func TestReadFindings_MissingTaskInjection(t *testing.T) {
	rf, _ := BuildReadFindings(&fakeStore{}, "", "")
	it := rf.(tool.InvokableTool)
	if _, err := it.InvokableRun(context.Background(), "{}"); err == nil {
		t.Fatal("task/host 注入缺失应报错")
	}
}

func TestWriteFinding_InjectionAndArgs(t *testing.T) {
	store := &fakeStore{}
	wf, err := BuildWriteFinding(store, "task-9", "hunter-7", "host-9", 42)
	if err != nil {
		t.Fatal(err)
	}
	args := `{"summary":"BAC in /admin","severity":"critical","cwe_id":"CWE-862",` +
		`"target":{"method":"GET","path":"/admin"},"evidence":{"repro_cmd":"curl ..."}}`
	out := invoke(t, wf, args)
	if !strings.Contains(out, "fk-1") {
		t.Fatalf("write_finding 应返回 id: %s", out)
	}
	// 断言注入字段（LLM 不可控）被正确填入
	s := store.saved
	if s.TaskID != "task-9" || s.Host != "host-9" {
		t.Errorf("task/host 注入错: %+v", s)
	}
	if s.HunterID == nil || *s.HunterID != "hunter-7" {
		t.Errorf("hunter 注入错: %+v", s.HunterID)
	}
	if s.SourceFlowID == nil || *s.SourceFlowID != 42 {
		t.Errorf("flowID 注入错: %+v", s.SourceFlowID)
	}
	// 断言 LLM 参数解析
	if s.Summary != "BAC in /admin" || s.CWEID != "CWE-862" {
		t.Errorf("参数解析错: %+v", s)
	}
	// target/evidence 应是 object 形态（非 string-encoded）
	if !strings.Contains(string(s.Target), `"path":"/admin"`) {
		t.Errorf("target 应为 object: %s", s.Target)
	}
}

func TestWriteFinding_SummaryRequired(t *testing.T) {
	wf, _ := BuildWriteFinding(&fakeStore{}, "task-1", "", "h", 0)
	it := wf.(tool.InvokableTool)
	if _, err := it.InvokableRun(context.Background(), `{"severity":"low"}`); err == nil {
		t.Fatal("缺 summary 应报错")
	}
}

func TestUpdateFinding(t *testing.T) {
	store := &fakeStore{}
	uf, err := BuildUpdateFinding(store)
	if err != nil {
		t.Fatal(err)
	}
	out := invoke(t, uf, `{"id":"f1","severity":"critical","target":{"path":"/x"}}`)
	if !strings.Contains(out, "ok") {
		t.Fatalf("update_finding 应返回 ok: %s", out)
	}
	if store.updated.id != "f1" || store.updated.severity != "critical" {
		t.Errorf("update 参数错: %+v", store.updated)
	}
	// target 以 object 形态透传；summary 未传应为空（保留原值语义）
	if !strings.Contains(string(store.updated.target), `"path":"/x"`) {
		t.Errorf("target 应为 object: %s", store.updated.target)
	}
	if store.updated.summary != "" {
		t.Errorf("未传 summary 应空，得到 %q", store.updated.summary)
	}
}

// TestUpdateFinding_DependsOn 验证收尾复盘补组合漏洞依赖：update_finding 传 depends_on → 透传到 store。
func TestUpdateFinding_DependsOn(t *testing.T) {
	store := &fakeStore{}
	uf, err := BuildUpdateFinding(store)
	if err != nil {
		t.Fatal(err)
	}
	// 复盘识别：RCE(f3) = 上传(f1) + 包含(f2) → 给 f3 补 depends_on
	out := invoke(t, uf, `{"id":"f3","depends_on":["f1","f2"]}`)
	if !strings.Contains(out, "ok") {
		t.Fatalf("update_finding 应返回 ok: %s", out)
	}
	if store.updated.id != "f3" {
		t.Errorf("id 应为 f3，得 %q", store.updated.id)
	}
	if len(store.updated.dependsOn) != 2 || store.updated.dependsOn[0] != "f1" || store.updated.dependsOn[1] != "f2" {
		t.Errorf("depends_on 应透传 [f1 f2]，得 %v", store.updated.dependsOn)
	}
}

func TestUpdateFinding_IDRequired(t *testing.T) {
	uf, _ := BuildUpdateFinding(&fakeStore{})
	it := uf.(tool.InvokableTool)
	if _, err := it.InvokableRun(context.Background(), `{"severity":"low"}`); err == nil {
		t.Fatal("缺 id 应报错")
	}
}
