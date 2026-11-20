// Package httpapi: 知识库 handler（前端「知识库」页）。
//
// 知识库是跨任务的长期知识：任务收尾沉淀与人工录入。页面提供浏览、检索预览（与 agent 的
// search_corpus 同一检索）、录入、Markdown 导入、修改与删除。每条知识归属一个场景或通用；
// 改了标题或正文的条目成为人工内容，此后任务收尾整合只能印证、不再改写它。
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"github.com/V3teran/liusha/internal/configstore"
	"github.com/V3teran/liusha/internal/corpus"
)

// CorpusAPI 是知识库 handler 依赖的窄接口；*corpus.Service 满足。
type CorpusAPI interface {
	Status(ctx context.Context) (corpus.Status, error)
	List(ctx context.Context, q corpus.ListQuery) (corpus.Page, error)
	Facets(ctx context.Context, q corpus.ListQuery) (corpus.Facets, error)
	Get(ctx context.Context, id string) (corpus.Entry, []corpus.Confirmation, error)
	Create(ctx context.Context, d corpus.Draft) (corpus.Entry, error)
	Update(ctx context.Context, id string, p corpus.Patch) (corpus.Entry, error)
	Delete(ctx context.Context, id string) error
	Search(ctx context.Context, q corpus.Query) (corpus.Result, error)
	Import(ctx context.Context, md string, scenario *string) (corpus.ImportResult, error)
}

// CorpusScenarios 是知识库 handler 解析场景名称与校验场景存在所需的窄接口。
type CorpusScenarios interface {
	ScenarioDetails(ctx context.Context) ([]configstore.ScenarioDetail, error)
}

const (
	defaultCorpusPageSize = 20
	maxCorpusPageSize     = 100
	// maxCorpusImportBytes 是一次导入的 Markdown 总量上限。
	maxCorpusImportBytes = 2 << 20
	maxCorpusImportFiles = 20
)

func registerCorpusRoutes(r gin.IRouter, d Deps) {
	if d.Corpus == nil || d.ScenarioStore == nil {
		return
	}
	h := corpusHandlers{kb: d.Corpus, scenarios: d.ScenarioStore}
	// 静态子路由先于 :id 注册
	r.GET("/corpus", h.list)
	r.GET("/corpus/facets", h.facets)
	r.GET("/corpus/status", h.status)
	r.POST("/corpus", h.create)
	r.POST("/corpus/search", h.search)
	r.POST("/corpus/import", h.importMarkdown)
	r.GET("/corpus/:id", uuidID, h.get)
	r.PATCH("/corpus/:id", uuidID, h.update)
	r.DELETE("/corpus/:id", uuidID, h.delete)
}

type corpusHandlers struct {
	kb        CorpusAPI
	scenarios CorpusScenarios
}

// scenarioNames 取全部现存场景的「标识 → 名称」。
func (h corpusHandlers) scenarioNames(ctx context.Context) (map[string]string, error) {
	all, err := h.scenarios.ScenarioDetails(ctx)
	if err != nil {
		return nil, err
	}
	names := make(map[string]string, len(all))
	for _, d := range all {
		names[d.Scenario.Code] = d.Scenario.Name
	}
	return names, nil
}

// scenarioJSON 写出条目的场景归属：null = 通用；场景已删除时只有标识与 deleted 标注。
func corpusScenarioJSON(code *string, names map[string]string) any {
	if code == nil {
		return nil
	}
	name, ok := names[*code]
	return gin.H{"code": *code, "name": name, "deleted": !ok}
}

func corpusEntryJSON(e corpus.Entry, names map[string]string) gin.H {
	return gin.H{
		"id": e.ID, "title": e.Title, "content": e.Content,
		"scenario": corpusScenarioJSON(e.Scenario, names),
		"source":   e.Source, "indexed": e.Indexed, "confirmations": e.Confirmations,
		"created_at": e.CreatedAt.Format(time.RFC3339), "updated_at": e.UpdatedAt.Format(time.RFC3339),
	}
}

// listQuery 解析列表与分面共用的筛选参数：search、scenario（标识）、general（只看通用）、source。
func corpusListQuery(c *gin.Context) (corpus.ListQuery, bool) {
	q := corpus.ListQuery{
		Search:   strings.TrimSpace(c.Query("search")),
		Scenario: strings.TrimSpace(c.Query("scenario")),
		General:  c.Query("general") == "true",
		Source:   c.Query("source"),
	}
	if err := q.Validate(); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return q, false
	}
	return q, true
}

// list 处理 GET /corpus?search=&scenario=&general=&source=&page=&size= → {items, total}（按更新时间倒序）。
func (h corpusHandlers) list(c *gin.Context) {
	q, ok := corpusListQuery(c)
	if !ok {
		return
	}
	page, size := parsePagination(c.Query("page"), c.Query("size"), defaultCorpusPageSize, maxCorpusPageSize)
	q.Offset, q.Limit = (page-1)*size, size
	ctx := c.Request.Context()
	res, err := h.kb.List(ctx, q)
	if err != nil {
		respondErr(c, http.StatusInternalServerError, err)
		return
	}
	names, err := h.scenarioNames(ctx)
	if err != nil {
		respondErr(c, http.StatusInternalServerError, err)
		return
	}
	items := make([]gin.H, 0, len(res.Items))
	for _, e := range res.Items {
		items = append(items, corpusEntryJSON(e, names))
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "total": res.Total})
}

// facets 处理 GET /corpus/facets（参数同列表）→ {scenarios, sources}：每个维度只受其它维度的筛选影响。
func (h corpusHandlers) facets(c *gin.Context) {
	q, ok := corpusListQuery(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	f, err := h.kb.Facets(ctx, q)
	if err != nil {
		respondErr(c, http.StatusInternalServerError, err)
		return
	}
	names, err := h.scenarioNames(ctx)
	if err != nil {
		respondErr(c, http.StatusInternalServerError, err)
		return
	}
	scenarios := make([]gin.H, 0, len(f.Scenarios))
	for _, s := range f.Scenarios {
		scenarios = append(scenarios, gin.H{"scenario": corpusScenarioJSON(s.Scenario, names), "count": s.Count})
	}
	sources := make([]gin.H, 0, len(f.Sources))
	for _, s := range f.Sources {
		sources = append(sources, gin.H{"source": s.Source, "count": s.Count})
	}
	c.JSON(http.StatusOK, gin.H{"scenarios": scenarios, "sources": sources})
}

// status 处理 GET /corpus/status → {mode, embedding_model, rerank_model, total, pending}。
func (h corpusHandlers) status(c *gin.Context) {
	st, err := h.kb.Status(c.Request.Context())
	if err != nil {
		respondErr(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"mode": st.Mode, "embedding_model": st.EmbeddingModel, "rerank_model": st.RerankModel,
		"total": st.Index.Total, "pending": st.Index.Pending,
	})
}

// get 处理 GET /corpus/:id → {entry, confirmations}。
func (h corpusHandlers) get(c *gin.Context) {
	ctx := c.Request.Context()
	e, confirmations, err := h.kb.Get(ctx, c.Param("id"))
	if err != nil {
		respondErr(c, http.StatusInternalServerError, err)
		return
	}
	names, err := h.scenarioNames(ctx)
	if err != nil {
		respondErr(c, http.StatusInternalServerError, err)
		return
	}
	cs := make([]gin.H, 0, len(confirmations))
	for _, cf := range confirmations {
		cs = append(cs, gin.H{"task_id": cf.TaskID, "task_goal": cf.TaskGoal, "confirmed_at": cf.ConfirmedAt.Format(time.RFC3339)})
	}
	c.JSON(http.StatusOK, gin.H{"entry": corpusEntryJSON(e, names), "confirmations": cs})
}

// scenarioRef 校验提交的场景：nil 或空串 = 通用；否则必须是现存场景，不存在写 400 并返回 ok=false。
func (h corpusHandlers) scenarioRef(c *gin.Context, code *string) (*string, bool) {
	if code == nil || strings.TrimSpace(*code) == "" {
		return nil, true
	}
	v := strings.TrimSpace(*code)
	names, err := h.scenarioNames(c.Request.Context())
	if err != nil {
		respondErr(c, http.StatusInternalServerError, err)
		return nil, false
	}
	if _, ok := names[v]; !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("场景 %s 不存在", v)})
		return nil, false
	}
	return &v, true
}

// respondCorpusErr 收敛知识库的客户端错误：不合法 400、正文重复与并发修改 409，其余交给 respondErr。
func respondCorpusErr(c *gin.Context, err error) {
	var dup *corpus.DuplicateError
	switch {
	case errors.Is(err, corpus.ErrInvalid):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.As(err, &dup):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error(), "existing_id": dup.ExistingID})
	case errors.Is(err, corpus.ErrDuplicate), errors.Is(err, corpus.ErrStale):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	default:
		respondErr(c, http.StatusInternalServerError, err)
	}
}

// create 处理 POST /corpus {title, content, scenario}（scenario 缺省或 null = 通用）→ 201 条目。
func (h corpusHandlers) create(c *gin.Context) {
	var req struct {
		Title    string  `json:"title"`
		Content  string  `json:"content"`
		Scenario *string `json:"scenario"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体格式错误"})
		return
	}
	scenario, ok := h.scenarioRef(c, req.Scenario)
	if !ok {
		return
	}
	e, err := h.kb.Create(c.Request.Context(), corpus.Draft{
		Title: req.Title, Content: req.Content, Scenario: scenario, Source: corpus.SourceExpert,
	})
	if err != nil {
		respondCorpusErr(c, err)
		return
	}
	h.respondEntry(c, http.StatusCreated, e)
}

func (h corpusHandlers) respondEntry(c *gin.Context, status int, e corpus.Entry) {
	names, err := h.scenarioNames(c.Request.Context())
	if err != nil {
		respondErr(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(status, corpusEntryJSON(e, names))
}

// update 处理 PATCH /corpus/:id {title?, content?, scenario?}：只改给出的字段；scenario 为 null 改为通用。
func (h corpusHandlers) update(c *gin.Context) {
	var raw map[string]json.RawMessage
	if err := c.ShouldBindJSON(&raw); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体格式错误"})
		return
	}
	var p corpus.Patch
	for key, val := range raw {
		var err error
		switch key {
		case "title":
			err = json.Unmarshal(val, &p.Title)
		case "content":
			err = json.Unmarshal(val, &p.Content)
		case "scenario":
			p.ScenarioSet = true
			err = json.Unmarshal(val, &p.Scenario)
		default:
			c.JSON(http.StatusBadRequest, gin.H{"error": "不支持修改字段 " + key})
			return
		}
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": key + " 格式错误"})
			return
		}
	}
	if p.ScenarioSet {
		scenario, ok := h.scenarioRef(c, p.Scenario)
		if !ok {
			return
		}
		p.Scenario = scenario
	}
	e, err := h.kb.Update(c.Request.Context(), c.Param("id"), p)
	if err != nil {
		respondCorpusErr(c, err)
		return
	}
	h.respondEntry(c, http.StatusOK, e)
}

// delete 处理 DELETE /corpus/:id → 204。
func (h corpusHandlers) delete(c *gin.Context) {
	if err := h.kb.Delete(c.Request.Context(), c.Param("id")); err != nil {
		respondErr(c, http.StatusInternalServerError, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// search 处理 POST /corpus/search {query, scenario, limit}：以 scenario 场景下的任务视角（空串 =
// 无场景的任务）执行与 search_corpus 相同的检索 → {mode, fallback, hits:[{entry, score}]}。
func (h corpusHandlers) search(c *gin.Context) {
	var req struct {
		Query    string `json:"query"`
		Scenario string `json:"scenario"`
		Limit    int    `json:"limit"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体格式错误"})
		return
	}
	scope := corpus.Scope{}
	if s := strings.TrimSpace(req.Scenario); s != "" {
		scope.Scenario = &s
	}
	ctx := c.Request.Context()
	res, err := h.kb.Search(ctx, corpus.Query{Text: req.Query, Scope: scope, Limit: req.Limit})
	if err != nil {
		respondCorpusErr(c, err)
		return
	}
	names, err := h.scenarioNames(ctx)
	if err != nil {
		respondErr(c, http.StatusInternalServerError, err)
		return
	}
	hits := make([]gin.H, 0, len(res.Hits))
	for _, hit := range res.Hits {
		hits = append(hits, gin.H{"entry": corpusEntryJSON(hit.Entry, names), "score": hit.Score})
	}
	c.JSON(http.StatusOK, gin.H{"mode": res.Mode, "fallback": res.Fallback, "hits": hits})
}

// importMarkdown 处理 POST /corpus/import（multipart：files 一个或多个 .md，scenario 可空 = 通用）
// → {imported, duplicates}。按标题切成条目作为人工内容写入；正文与已有条目相同的跳过。
func (h corpusHandlers) importMarkdown(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxCorpusImportBytes+multipartOverhead)
	form, err := c.MultipartForm()
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": fmt.Sprintf("导入内容超过 %d MB", maxCorpusImportBytes>>20)})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式错误：需要 multipart 表单"})
		return
	}
	files := form.File["files"]
	if len(files) == 0 || len(files) > maxCorpusImportFiles {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("请选择 1–%d 个 Markdown 文件", maxCorpusImportFiles)})
		return
	}
	var scenarioField *string
	if v := form.Value["scenario"]; len(v) > 0 {
		scenarioField = &v[0]
	}
	scenario, ok := h.scenarioRef(c, scenarioField)
	if !ok {
		return
	}
	docs := make([]string, 0, len(files))
	for _, fh := range files {
		if !strings.HasSuffix(strings.ToLower(fh.Filename), ".md") {
			c.JSON(http.StatusBadRequest, gin.H{"error": fh.Filename + " 不是 Markdown（.md）文件"})
			return
		}
		f, err := fh.Open()
		if err != nil {
			respondErr(c, http.StatusInternalServerError, fmt.Errorf("读取上传文件: %w", err))
			return
		}
		b, err := io.ReadAll(f)
		_ = f.Close()
		if err != nil {
			respondErr(c, http.StatusInternalServerError, fmt.Errorf("读取上传文件: %w", err))
			return
		}
		if !utf8.Valid(b) {
			c.JSON(http.StatusBadRequest, gin.H{"error": fh.Filename + " 不是 UTF-8 编码"})
			return
		}
		docs = append(docs, string(b))
	}
	var total corpus.ImportResult
	for i, md := range docs {
		res, err := h.kb.Import(c.Request.Context(), md, scenario)
		total.Imported += res.Imported
		total.Duplicates += res.Duplicates
		if err != nil {
			respondCorpusErr(c, fmt.Errorf("%s: %w", files[i].Filename, err))
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"imported": total.Imported, "duplicates": total.Duplicates})
}
