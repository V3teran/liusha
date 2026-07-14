package einotools

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"

	"github.com/V3teran/liusha/internal/corpus"
	"github.com/V3teran/liusha/internal/credential"
)

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

// ---- corpus ----

type fakeCorpusStore struct {
	hits  []corpus.Entry // SearchHybrid 返回
	added corpus.Entry   // Add 记录最近一次
}

func (f *fakeCorpusStore) SearchHybrid(_ context.Context, _ string, _ []float32, _ []string, _, _ int, _ corpus.Reranker) ([]corpus.Entry, error) {
	return f.hits, nil
}
func (f *fakeCorpusStore) Add(_ context.Context, e corpus.Entry) (corpus.Entry, error) {
	e.ID = "corp-1"
	f.added = e
	return e, nil
}

func TestSearchCorpus(t *testing.T) {
	store := &fakeCorpusStore{hits: []corpus.Entry{
		{ID: "c1", Title: "CAS SSO 登录逆向", Content: "先逆前端 js 拿加密逻辑", Source: corpus.SourceExpert},
	}}
	// embedder/reranker 传 nil → 降级路径（纯 sparse + 合并序），工具仍应正常返回。
	sc, err := BuildSearchCorpus(store, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	out := invoke(t, sc, `{"query":"某 SSO 怎么登录"}`)
	if !strings.Contains(out, "先逆前端 js 拿加密逻辑") {
		t.Fatalf("search_corpus 输出缺知识: %s", out)
	}
}

func TestWriteCorpus_InjectsSourceAndTask(t *testing.T) {
	store := &fakeCorpusStore{}
	wc, err := BuildWriteCorpus(store, nil, "task-9") // emb nil → 不 embed
	if err != nil {
		t.Fatal(err)
	}
	invoke(t, wc, `{"title":"SSO 逆向套路","content":"逆前端 js","tags":["sso:cas"]}`)
	if store.added.Source != corpus.SourceAgent || store.added.SourceTaskID != "task-9" {
		t.Errorf("source/task 闭包注入错: %+v", store.added)
	}
	if store.added.Title != "SSO 逆向套路" || store.added.Content != "逆前端 js" {
		t.Errorf("corpus 参数错: %+v", store.added)
	}
}

func TestWriteCorpus_RejectsEmpty(t *testing.T) {
	wc, _ := BuildWriteCorpus(&fakeCorpusStore{}, nil, "task-1")
	it := wc.(tool.InvokableTool)
	if _, err := it.InvokableRun(context.Background(), `{"title":"x"}`); err == nil {
		t.Fatal("content 空应报错")
	}
}
