package einotools

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"

	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/lesson"
)

// ---- notes ----

type fakeNoteStore struct {
	blob     []byte
	appended []byte
}

func (f *fakeNoteStore) ReadNotes(_ context.Context, _, _ string) ([]byte, error) { return f.blob, nil }
func (f *fakeNoteStore) AppendNote(_ context.Context, _, _ string, entry []byte) error {
	f.appended = entry
	return nil
}

func TestReadNotes(t *testing.T) {
	store := &fakeNoteStore{blob: []byte(`[{"content":"怪癖A"}]`)}
	rn, err := BuildReadNotes(store, "owner-1", "host-1", "h1")
	if err != nil {
		t.Fatal(err)
	}
	out := invoke(t, rn, "{}")
	if !strings.Contains(out, "怪癖A") {
		t.Fatalf("read_notes 输出缺内容: %s", out)
	}
}

func TestReadNotes_EmptyDefaultsArray(t *testing.T) {
	rn, _ := BuildReadNotes(&fakeNoteStore{}, "owner-1", "host-1", "h1")
	out := invoke(t, rn, "{}")
	if !strings.Contains(out, "[]") {
		t.Fatalf("空 note 应返回 []，得到 %s", out)
	}
}

func TestReadNotes_MissingOwnerInjection(t *testing.T) {
	rn, _ := BuildReadNotes(&fakeNoteStore{}, "", "", "")
	it := rn.(tool.InvokableTool)
	if _, err := it.InvokableRun(context.Background(), "{}"); err == nil {
		t.Fatal("owner/host 注入缺失应报错")
	}
}

func TestWriteNote_InjectsHunterID(t *testing.T) {
	store := &fakeNoteStore{}
	wn, err := BuildWriteNote(store, "owner-1", "host-1", "hunter-9")
	if err != nil {
		t.Fatal(err)
	}
	invoke(t, wn, `{"content":"死路：/admin 403"}`)
	if !strings.Contains(string(store.appended), "死路：/admin 403") {
		t.Errorf("note content 未写入: %s", store.appended)
	}
	// hunter_id 闭包注入（LLM 不可控）
	if !strings.Contains(string(store.appended), "hunter-9") {
		t.Errorf("hunter_id 注入缺失: %s", store.appended)
	}
}

func TestWriteNote_ContentRequired(t *testing.T) {
	wn, _ := BuildWriteNote(&fakeNoteStore{}, "o", "h", "x")
	it := wn.(tool.InvokableTool)
	if _, err := it.InvokableRun(context.Background(), `{}`); err == nil {
		t.Fatal("缺 content 应报错")
	}
}

// ---- credentials ----

type fakeCredStore struct {
	ids   []credential.Identity
	saved map[string][]credential.Identity
}

func (f *fakeCredStore) GetIdentitiesByHost(_ context.Context, _ string) ([]credential.Identity, error) {
	return f.ids, nil
}
func (f *fakeCredStore) BatchSave(_ context.Context, byHost map[string][]credential.Identity, _ int) error {
	f.saved = byHost
	return nil
}

func TestReadCredentials(t *testing.T) {
	store := &fakeCredStore{ids: []credential.Identity{
		{Name: "admin", Role: "admin", Credentials: []credential.Credential{
			{Type: credential.TypeHeaders, Key: "Cookie", Value: "PHPSESSID=abc"},
		}},
	}}
	rc, err := BuildReadCredentials(store, "host-1")
	if err != nil {
		t.Fatal(err)
	}
	out := invoke(t, rc, "{}")
	if !strings.Contains(out, "admin") || !strings.Contains(out, "PHPSESSID=abc") {
		t.Fatalf("read_credentials 输出缺身份: %s", out)
	}
}

func TestWriteCredential_InjectionAndValidation(t *testing.T) {
	store := &fakeCredStore{}
	wc, err := BuildWriteCredential(store, "host-7")
	if err != nil {
		t.Fatal(err)
	}
	out := invoke(t, wc, `{"name":"tester","role":"user","credentials":[{"type":"headers","key":"Cookie","value":"sid=1"}]}`)
	if !strings.Contains(out, "saved") {
		t.Fatalf("write_credential 应返回 saved: %s", out)
	}
	// host 闭包注入：写入 key 应是注入的 host-7（LLM 不传 host）
	got, ok := store.saved["host-7"]
	if !ok || len(got) != 1 || got[0].Name != "tester" {
		t.Errorf("host 注入或身份错: %+v", store.saved)
	}
	if got[0].Credentials[0].Type != credential.TypeHeaders {
		t.Errorf("credential type 错: %+v", got[0].Credentials)
	}
}

func TestWriteCredential_RejectsAnonymous(t *testing.T) {
	wc, _ := BuildWriteCredential(&fakeCredStore{}, "host-1")
	it := wc.(tool.InvokableTool)
	_, err := it.InvokableRun(context.Background(), `{"name":"anonymous","credentials":[{"type":"headers","key":"Cookie","value":"x"}]}`)
	if err == nil {
		t.Fatal("name=anonymous 应被拒")
	}
}

func TestWriteCredential_RejectsBadType(t *testing.T) {
	wc, _ := BuildWriteCredential(&fakeCredStore{}, "host-1")
	it := wc.(tool.InvokableTool)
	_, err := it.InvokableRun(context.Background(), `{"name":"x","credentials":[{"type":"cookie","key":"k","value":"v"}]}`)
	if err == nil {
		t.Fatal("非法 type 应被拒")
	}
}

// ---- lessons ----

type fakeLessonStore struct {
	list  []lesson.Lesson
	added lesson.Lesson
}

func (f *fakeLessonStore) ListByHost(_ context.Context, _ string, _ int) ([]lesson.Lesson, error) {
	return f.list, nil
}
func (f *fakeLessonStore) Add(_ context.Context, l lesson.Lesson) (lesson.Lesson, error) {
	l.ID = "les-1"
	f.added = l
	return l, nil
}

func TestReadLessons(t *testing.T) {
	store := &fakeLessonStore{list: []lesson.Lesson{
		{ID: "l1", Kind: lesson.KindLesson, Content: "DVWA 先 GET 拿 token", Priority: 7},
	}}
	rl, err := BuildReadLessons(store, "host-1")
	if err != nil {
		t.Fatal(err)
	}
	out := invoke(t, rl, "{}")
	if !strings.Contains(out, "DVWA 先 GET 拿 token") {
		t.Fatalf("read_lessons 输出缺经验: %s", out)
	}
}

func TestWriteLesson_DefaultKindKeepsHost(t *testing.T) {
	store := &fakeLessonStore{}
	wl, err := BuildWriteLesson(store, "host-1")
	if err != nil {
		t.Fatal(err)
	}
	invoke(t, wl, `{"content":"默认 admin:password","priority":6}`)
	if store.added.Host != "host-1" || store.added.Kind != lesson.KindLesson {
		t.Errorf("默认 kind 应保留注入 host: %+v", store.added)
	}
	if store.added.Content != "默认 admin:password" || store.added.Priority != 6 {
		t.Errorf("lesson 参数错: %+v", store.added)
	}
}

func TestWriteLesson_HintForcesGlobalHost(t *testing.T) {
	store := &fakeLessonStore{}
	wl, _ := BuildWriteLesson(store, "host-1")
	invoke(t, wl, `{"content":"价格篡改 ≥10% 才算","kind":"hint"}`)
	if store.added.Host != lesson.HostGlobalHint || store.added.Kind != lesson.KindHint {
		t.Errorf("hint 应强制全局 host: %+v", store.added)
	}
}

func TestWriteLesson_BadKind(t *testing.T) {
	wl, _ := BuildWriteLesson(&fakeLessonStore{}, "host-1")
	it := wl.(tool.InvokableTool)
	if _, err := it.InvokableRun(context.Background(), `{"content":"x","kind":"bogus"}`); err == nil {
		t.Fatal("非法 kind 应报错")
	}
}
