package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/configstore"
	"github.com/V3teran/liusha/internal/corpus"
	"github.com/V3teran/liusha/internal/retrievalstatus"
	"github.com/V3teran/liusha/internal/scenariostore"
)

const corpusID = "11111111-2222-4333-8444-555555555555"

// fakeCorpus 是知识库的内存假实现：只有一条 ID 为 corpusID 的条目。
type fakeCorpus struct {
	semantic bool // Status 返回语义模式（降级合并用）
	entry    corpus.Entry
	listed   corpus.ListQuery
	created  corpus.Draft
	patched  corpus.Patch
	searched corpus.Query
	imported []string
	importSc *string
	dupOn    string // 正文等于它时 Create 报重复
}

func (f *fakeCorpus) Status(context.Context) (corpus.Status, error) {
	st := corpus.Status{Mode: corpus.ModeLexical, Index: corpus.IndexStats{Total: 1}}
	if f.semantic {
		st.Mode = corpus.ModeSemantic
		st.EmbeddingModel, st.RerankModel = "e", "r"
	}
	return st, nil
}

func (f *fakeCorpus) List(_ context.Context, q corpus.ListQuery) (corpus.Page, error) {
	if err := q.Validate(); err != nil {
		return corpus.Page{}, err
	}
	f.listed = q
	return corpus.Page{Items: []corpus.Entry{f.entry}, Total: 1}, nil
}

func (f *fakeCorpus) Facets(_ context.Context, q corpus.ListQuery) (corpus.Facets, error) {
	ctf := "ctf"
	return corpus.Facets{
		Scenarios: []corpus.ScenarioFacet{{Count: 2}, {Scenario: &ctf, Count: 1}},
		Sources:   []corpus.SourceFacet{{Source: corpus.SourceAgent, Count: 3}},
	}, q.Validate()
}

func (f *fakeCorpus) Get(_ context.Context, id string) (corpus.Entry, []corpus.Confirmation, error) {
	if id != corpusID {
		return corpus.Entry{}, nil, fmt.Errorf("%w: %w", corpus.ErrNotFound, pgx.ErrNoRows)
	}
	return f.entry, []corpus.Confirmation{{TaskID: "t1", TaskGoal: "目标", ConfirmedAt: time.Unix(0, 0)}}, nil
}

func (f *fakeCorpus) Create(_ context.Context, d corpus.Draft) (corpus.Entry, error) {
	d, err := d.Normalize()
	if err != nil {
		return corpus.Entry{}, err
	}
	if d.Content == f.dupOn {
		return corpus.Entry{}, &corpus.DuplicateError{ExistingID: corpusID}
	}
	f.created = d
	return corpus.Entry{ID: corpusID, Title: d.Title, Content: d.Content, Scenario: d.Scenario, Source: d.Source}, nil
}

func (f *fakeCorpus) Update(_ context.Context, id string, p corpus.Patch) (corpus.Entry, error) {
	if id != corpusID {
		return corpus.Entry{}, fmt.Errorf("%w: %w", corpus.ErrNotFound, pgx.ErrNoRows)
	}
	f.patched = p
	return f.entry, nil
}

func (f *fakeCorpus) Delete(_ context.Context, id string) error {
	if id != corpusID {
		return fmt.Errorf("%w: %w", corpus.ErrNotFound, pgx.ErrNoRows)
	}
	return nil
}

func (f *fakeCorpus) Search(_ context.Context, q corpus.Query) (corpus.Result, error) {
	if strings.TrimSpace(q.Text) == "" {
		return corpus.Result{}, fmt.Errorf("%w：检索内容不能为空", corpus.ErrInvalid)
	}
	f.searched = q
	return corpus.Result{Mode: corpus.ModeSemantic, Hits: []corpus.Hit{{Entry: f.entry, Score: 0.8}}}, nil
}

func (f *fakeCorpus) PreviewImport(_ context.Context, docs []string) ([][]corpus.ImportSection, error) {
	out := make([][]corpus.ImportSection, len(docs))
	for i, md := range docs {
		for j, sec := range corpus.SplitMarkdown(md) {
			out[i] = append(out[i], corpus.ImportSection{Section: sec, Duplicate: j > 0})
		}
	}
	return out, nil
}

func (f *fakeCorpus) Import(_ context.Context, md string, scenario *string) (corpus.ImportResult, error) {
	f.imported = append(f.imported, md)
	f.importSc = scenario
	return corpus.ImportResult{Imported: 2, Duplicates: 1}, nil
}

func newCorpusRouter(t *testing.T) (*gin.Engine, *fakeCorpus) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	ctf := "ctf"
	kb := &fakeCorpus{entry: corpus.Entry{ID: corpusID, Title: "诱饵", Content: "正文", Scenario: &ctf,
		Source: corpus.SourceAgent, Confirmations: 2}}
	scenarios := &fakeScenarios{items: []configstore.ScenarioDetail{
		{Scenario: scenariostore.Scenario{Code: "ctf", Name: "CTF"}},
	}}
	r := gin.New()
	registerCorpusRoutes(r, Deps{Corpus: kb, ScenarioStore: scenarios})
	return r, kb
}

func corpusBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out), w.Body.String())
	return out
}

func TestCorpusList_ParsesFiltersAndNamesScenario(t *testing.T) {
	r, kb := newCorpusRouter(t)

	w := serve(r, http.MethodGet, "/corpus?search=%E8%AF%B1&scenario=ctf&source=agent&page=2&size=10", "")

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, corpus.ListQuery{Search: "诱", Scenario: "ctf", Source: "agent", Offset: 10, Limit: 10}, kb.listed)
	item := corpusBody(t, w)["items"].([]any)[0].(map[string]any)
	assert.Equal(t, map[string]any{"code": "ctf", "name": "CTF", "deleted": false}, item["scenario"])
	assert.EqualValues(t, 2, item["confirmations"])
}

func TestCorpusList_RejectsBadFilters(t *testing.T) {
	r, _ := newCorpusRouter(t)
	for _, q := range []string{"scenario=ctf&general=true", "source=bogus"} {
		w := serve(r, http.MethodGet, "/corpus?"+q, "")
		assert.Equal(t, http.StatusBadRequest, w.Code, q)
	}
}

func TestCorpusFacets_GeneralIsNull(t *testing.T) {
	r, _ := newCorpusRouter(t)

	w := serve(r, http.MethodGet, "/corpus/facets", "")

	require.Equal(t, http.StatusOK, w.Code)
	sc := corpusBody(t, w)["scenarios"].([]any)
	assert.Nil(t, sc[0].(map[string]any)["scenario"], "通用知识的场景为 null")
	assert.EqualValues(t, 2, sc[0].(map[string]any)["count"])
}

func TestCorpusGet(t *testing.T) {
	r, _ := newCorpusRouter(t)

	w := serve(r, http.MethodGet, "/corpus/"+corpusID, "")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Len(t, corpusBody(t, w)["confirmations"], 1)

	assert.Equal(t, http.StatusNotFound, serve(r, http.MethodGet, "/corpus/22222222-2222-4333-8444-555555555555", "").Code)
	assert.Equal(t, http.StatusBadRequest, serve(r, http.MethodGet, "/corpus/not-a-uuid", "").Code)
}

func TestCorpusCreate(t *testing.T) {
	r, kb := newCorpusRouter(t)

	w := serve(r, http.MethodPost, "/corpus", `{"title":"新","content":"正文","scenario":"ctf"}`)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	assert.Equal(t, corpus.SourceExpert, kb.created.Source, "页面录入的是人工内容")
	assert.Equal(t, "ctf", *kb.created.Scenario)

	w = serve(r, http.MethodPost, "/corpus", `{"title":"新","content":"正文2"}`)
	require.Equal(t, http.StatusCreated, w.Code)
	assert.Nil(t, kb.created.Scenario, "不给场景即通用")

	assert.Equal(t, http.StatusBadRequest, serve(r, http.MethodPost, "/corpus", `{"title":"新","content":"x","scenario":"nope"}`).Code, "场景不存在")
	assert.Equal(t, http.StatusBadRequest, serve(r, http.MethodPost, "/corpus", `{"title":"","content":"x"}`).Code)

	kb.dupOn = "重复"
	w = serve(r, http.MethodPost, "/corpus", `{"title":"新","content":"重复"}`)
	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Equal(t, corpusID, corpusBody(t, w)["existing_id"])
}

func TestCorpusUpdate(t *testing.T) {
	r, kb := newCorpusRouter(t)

	w := serve(r, http.MethodPatch, "/corpus/"+corpusID, `{"scenario":null}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.True(t, kb.patched.ScenarioSet)
	assert.Nil(t, kb.patched.Scenario, "null 即改为通用")
	assert.Nil(t, kb.patched.Title, "未给出的字段不改")

	w = serve(r, http.MethodPatch, "/corpus/"+corpusID, `{"content":"新正文"}`)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "新正文", *kb.patched.Content)
	assert.False(t, kb.patched.ScenarioSet)

	assert.Equal(t, http.StatusBadRequest, serve(r, http.MethodPatch, "/corpus/"+corpusID, `{"source":"agent"}`).Code, "来源不可直接修改")
	assert.Equal(t, http.StatusBadRequest, serve(r, http.MethodPatch, "/corpus/"+corpusID, `{"scenario":"nope"}`).Code)
	assert.Equal(t, http.StatusNotFound, serve(r, http.MethodPatch, "/corpus/22222222-2222-4333-8444-555555555555", `{"title":"x"}`).Code)
}

func TestCorpusDelete(t *testing.T) {
	r, _ := newCorpusRouter(t)
	assert.Equal(t, http.StatusNoContent, serve(r, http.MethodDelete, "/corpus/"+corpusID, "").Code)
	assert.Equal(t, http.StatusNotFound, serve(r, http.MethodDelete, "/corpus/22222222-2222-4333-8444-555555555555", "").Code)
}

func TestCorpusSearch(t *testing.T) {
	r, kb := newCorpusRouter(t)

	w := serve(r, http.MethodPost, "/corpus/search", `{"query":"诱饵","scenario":"ctf","limit":3}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, "ctf", *kb.searched.Scope.Scenario)
	assert.Equal(t, 3, kb.searched.Limit)
	body := corpusBody(t, w)
	assert.Equal(t, "semantic", body["mode"])
	assert.InDelta(t, 0.8, body["hits"].([]any)[0].(map[string]any)["score"], 1e-9)

	w = serve(r, http.MethodPost, "/corpus/search", `{"query":"诱饵"}`)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Nil(t, kb.searched.Scope.Scenario, "不给场景即无场景任务的视角")

	assert.Equal(t, http.StatusBadRequest, serve(r, http.MethodPost, "/corpus/search", `{"query":"  "}`).Code)
}

func corpusImportBody(t *testing.T, files map[string]string, scenario string) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for name, content := range files {
		fw, err := mw.CreateFormFile("files", name)
		require.NoError(t, err)
		_, _ = fw.Write([]byte(content))
	}
	if scenario != "" {
		require.NoError(t, mw.WriteField("scenario", scenario))
	}
	require.NoError(t, mw.Close())
	return &buf, mw.FormDataContentType()
}

func TestCorpusImport(t *testing.T) {
	r, kb := newCorpusRouter(t)
	post := func(files map[string]string, scenario string) *httptest.ResponseRecorder {
		body, ct := corpusImportBody(t, files, scenario)
		req := httptest.NewRequest(http.MethodPost, "/corpus/import", body)
		req.Header.Set("Content-Type", ct)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	w := post(map[string]string{"a.md": "# 甲\n内容"}, "ctf")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, map[string]any{"imported": float64(2), "duplicates": float64(1)}, corpusBody(t, w))
	assert.Equal(t, "ctf", *kb.importSc)

	assert.Len(t, kb.imported, 1)

	body, ct := corpusImportBody(t, map[string]string{"b.md": "# 甲\n一\n# 乙\n二"}, "")
	mw := httptest.NewRequest(http.MethodPost, "/corpus/import", body)
	mw.Header.Set("Content-Type", ct)
	_ = mw.ParseMultipartForm(1 << 20)
	mw.MultipartForm.Value["dry_run"] = []string{"true"}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, mw)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, []any{
		map[string]any{"file": "b.md", "title": "甲", "content": "一", "duplicate": false},
		map[string]any{"file": "b.md", "title": "乙", "content": "二", "duplicate": true},
	}, corpusBody(t, w)["sections"])
	assert.Len(t, kb.imported, 1, "预览不写入")

	assert.Equal(t, http.StatusBadRequest, post(map[string]string{"a.txt": "x"}, "").Code, "只收 .md")
	assert.Equal(t, http.StatusBadRequest, post(map[string]string{"a.md": "\xff\xfe"}, "").Code, "只收 UTF-8")
	assert.Equal(t, http.StatusBadRequest, post(map[string]string{"a.md": "x"}, "nope").Code, "场景不存在")
	assert.Equal(t, http.StatusBadRequest, post(map[string]string{}, "").Code, "至少一个文件")
}

// fakeHealthReader 记录读取，返回预置降级记录。
type fakeHealthReader struct {
	rec retrievalstatus.Record
	ok  bool
}

func (f *fakeHealthReader) Get(context.Context) (retrievalstatus.Record, bool, error) {
	return f.rec, f.ok, nil
}

// TestCorpusStatus_DegradedMerge：语义配置在但最近一次语义调用失败（如密钥欠费）时，
// 状态响应带 degraded 与 degraded_at；健康（无记录）与字面模式不带。
func TestCorpusStatus_DegradedMerge(t *testing.T) {
	at := time.Date(2026, 1, 1, 8, 0, 0, 0, time.UTC)
	fc := &fakeCorpus{semantic: true}
	fc.entry = corpus.Entry{ID: corpusID, Title: "T", Content: "C"}
	srv := newTestServer(t, Deps{
		Corpus:          fc,
		ScenarioStore:   &fakeScenarios{},
		RetrievalHealth: &fakeHealthReader{ok: true, rec: retrievalstatus.Record{Reason: "402 上游欠费", At: at}},
	})
	defer srv.Close()

	code, body := doJSON(t, "GET", srv.URL+"/api/corpus/status", nil)
	if code != 200 {
		t.Fatalf("status=%d", code)
	}
	if body["mode"] != "semantic" {
		t.Fatalf("mode=%v", body["mode"])
	}
	if body["degraded"] != "402 上游欠费" || body["degraded_at"] == nil {
		t.Fatalf("降级信息未合并: %v", body)
	}

	// 健康（无记录）：不带降级字段
	srv2 := newTestServer(t, Deps{
		Corpus:          fc,
		ScenarioStore:   &fakeScenarios{},
		RetrievalHealth: &fakeHealthReader{},
	})
	defer srv2.Close()
	code, body = doJSON(t, "GET", srv2.URL+"/api/corpus/status", nil)
	if code != 200 || body["degraded"] != nil {
		t.Fatalf("健康不应带降级: %v", body)
	}

	// 字面模式（未配置）：带 unavailable，不带 degraded
	fc2 := &fakeCorpus{}
	srv3 := newTestServer(t, Deps{
		Corpus:          fc2,
		ScenarioStore:   &fakeScenarios{},
		RetrievalHealth: &fakeHealthReader{ok: true, rec: retrievalstatus.Record{Reason: "x", At: at}},
	})
	defer srv3.Close()
	code, body = doJSON(t, "GET", srv3.URL+"/api/corpus/status", nil)
	if code != 200 || body["degraded"] != nil {
		t.Fatalf("字面模式不应带降级: %v", body)
	}
}
