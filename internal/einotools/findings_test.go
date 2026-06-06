package einotools

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"

	"github.com/V3teran/liusha/internal/finding"
)

// fakeStore 同时满足 FindingReader + FindingWriter，记录收到的写入用于断言注入字段。
type fakeStore struct {
	list  []finding.VulnFinding
	saved finding.VulnFinding
}

func (f *fakeStore) ListByOwnerAndHost(_ context.Context, _, _, _ string, _ int) ([]finding.VulnFinding, error) {
	return f.list, nil
}
func (f *fakeStore) Save(_ context.Context, v finding.VulnFinding) (finding.VulnFinding, error) {
	v.ID = "fk-1"
	f.saved = v
	return v, nil
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
	rf, err := BuildReadFindings(store, "active_scan", "owner-1", "host-1")
	if err != nil {
		t.Fatal(err)
	}
	out := invoke(t, rf, "{}")
	if !strings.Contains(out, "f1") || !strings.Contains(out, "SQLi in /user") {
		t.Fatalf("read_findings 输出缺 finding: %s", out)
	}
}

func TestReadFindings_MissingOwnerInjection(t *testing.T) {
	rf, _ := BuildReadFindings(&fakeStore{}, "", "", "")
	it := rf.(tool.InvokableTool)
	if _, err := it.InvokableRun(context.Background(), "{}"); err == nil {
		t.Fatal("owner 注入缺失应报错")
	}
}

func TestWriteFinding_InjectionAndArgs(t *testing.T) {
	store := &fakeStore{}
	wf, err := BuildWriteFinding(store, "active_scan", "owner-9", "hunter-7", "host-9", 42)
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
	if s.OwnerType != "active_scan" || s.OwnerID != "owner-9" || s.Host != "host-9" {
		t.Errorf("owner/host 注入错: %+v", s)
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
	wf, _ := BuildWriteFinding(&fakeStore{}, "active_scan", "o", "", "h", 0)
	it := wf.(tool.InvokableTool)
	if _, err := it.InvokableRun(context.Background(), `{"severity":"low"}`); err == nil {
		t.Fatal("缺 summary 应报错")
	}
}
