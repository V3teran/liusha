package einotools

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"

	"github.com/V3teran/liusha/internal/lead"
)

// fakeLeadStore 满足 LeadAdder，记录最近一次 Append 入参用于断言注入字段。
type fakeLeadStore struct {
	host  string
	entry lead.Entry
}

func (f *fakeLeadStore) Append(_ context.Context, host string, e lead.Entry) error {
	f.host = host
	f.entry = e
	return nil
}

func TestWriteLead_InjectionAndArgs(t *testing.T) {
	store := &fakeLeadStore{}
	wl, err := BuildWriteLead(store, "target.com", "hunter-1", "task-1")
	if err != nil {
		t.Fatal(err)
	}
	out := invoke(t, wl, `{"kind":"clue","note":"/admin/backup 疑似可访问，未验证"}`)
	if !strings.Contains(out, "ok") {
		t.Fatalf("write_lead 应返回 ok: %s", out)
	}
	if store.host != "target.com" {
		t.Errorf("host 注入错: %q", store.host)
	}
	if store.entry.HunterID != "hunter-1" || store.entry.SourceTaskID != "task-1" {
		t.Errorf("hunter/source_task 注入错: %+v", store.entry)
	}
	if store.entry.Kind != lead.KindClue || store.entry.Note != "/admin/backup 疑似可访问，未验证" {
		t.Errorf("参数解析错: %+v", store.entry)
	}
}

func TestWriteLead_MissingHostInjection(t *testing.T) {
	wl, _ := BuildWriteLead(&fakeLeadStore{}, "", "h", "t")
	it := wl.(tool.InvokableTool)
	if _, err := it.InvokableRun(context.Background(), `{"kind":"fact","note":"x"}`); err == nil {
		t.Fatal("host 注入缺失应报错")
	}
}

func TestWriteLead_RejectsBadKind(t *testing.T) {
	wl, _ := BuildWriteLead(&fakeLeadStore{}, "h", "hid", "tid")
	it := wl.(tool.InvokableTool)
	if _, err := it.InvokableRun(context.Background(), `{"kind":"bogus","note":"x"}`); err == nil {
		t.Fatal("非法 kind 应报错")
	}
}

func TestWriteLead_NoteRequired(t *testing.T) {
	wl, _ := BuildWriteLead(&fakeLeadStore{}, "h", "hid", "tid")
	it := wl.(tool.InvokableTool)
	if _, err := it.InvokableRun(context.Background(), `{"kind":"fact","note":""}`); err == nil {
		t.Fatal("空 note 应报错")
	}
}
