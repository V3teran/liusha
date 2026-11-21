//go:build integration

package corpus

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/dbtest"
)

// fakeEmbedder 按文本里是否出现若干关键词给出确定的向量（1024 维，对应维度置 1）。
type fakeEmbedder struct{ fail atomic.Bool }

var embedKeywords = []string{"诱饵", "基线", "robots", "解码"}

func (f *fakeEmbedder) vec(text string) []float32 {
	v := make([]float32, 1024)
	v[1023] = 0.1 // 不含任何关键词的文本也有非零向量
	for i, k := range embedKeywords {
		if strings.Contains(text, k) {
			v[i] = 1
		}
	}
	return v
}

func (f *fakeEmbedder) EmbedQuery(_ context.Context, text string) ([]float32, error) {
	if f.fail.Load() {
		return nil, errors.New("向量服务不可用")
	}
	return f.vec(text), nil
}

func (f *fakeEmbedder) EmbedPassage(_ context.Context, texts []string) ([][]float32, error) {
	if f.fail.Load() {
		return nil, errors.New("向量服务不可用")
	}
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = f.vec(t)
	}
	return out, nil
}

// fakeReranker 以查询与文档共有的关键词判分：共有任一关键词 0.9，否则 0.2（模拟真实模型给无关文本的底分）。
type fakeReranker struct{ fail atomic.Bool }

func (f *fakeReranker) Rerank(_ context.Context, query string, docs []string, _ int) ([]Ranked, error) {
	if f.fail.Load() {
		return nil, errors.New("重排服务不可用")
	}
	out := make([]Ranked, 0, len(docs))
	for i, d := range docs {
		score := 0.2
		for _, k := range embedKeywords {
			if strings.Contains(query, k) && strings.Contains(d, k) {
				score = 0.9
			}
		}
		out = append(out, Ranked{Index: i, Score: score})
	}
	return out, nil
}

func newService(t *testing.T, models Models) (*Service, *Store) {
	t.Helper()
	store := NewStore(dbtest.NewPgPool(t))
	return NewService(store, models, zerolog.Nop()), store
}

func semanticModels() (Models, *fakeEmbedder, *fakeReranker) {
	e, r := &fakeEmbedder{}, &fakeReranker{}
	return Models{Embedder: e, Reranker: r, EmbeddingModel: "fake-embed", RerankModel: "fake-rerank"}, e, r
}

func ptr(s string) *string { return &s }

func mustCreate(t *testing.T, s *Service, d Draft) Entry {
	t.Helper()
	if d.Source == "" {
		d.Source = SourceAgent
	}
	e, err := s.Create(context.Background(), d)
	require.NoError(t, err)
	return e
}

func titles(hits []Hit) []string {
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.Entry.Title
	}
	return out
}

// seedTask 插入一个最小任务行（印证表外键指向 task）。
func seedTask(t *testing.T, s *Store) string {
	t.Helper()
	var id string
	require.NoError(t, s.pool.QueryRow(context.Background(), `
		WITH a AS (INSERT INTO assignment (source, title) VALUES ('direct', 't') RETURNING id)
		INSERT INTO task (goal, assignment_id) SELECT 'g', a.id FROM a RETURNING id`).Scan(&id))
	return id
}

func TestCorpusTerms_ChineseBigramsAndWords(t *testing.T) {
	pool := dbtest.NewPgPool(t)
	var terms []string
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT corpus_terms('先查 robots.txt 定位隐藏目录，C++ 与 a 1')`).Scan(&terms))
	assert.Subset(t, terms, []string{"robots.txt", "c++", "先查", "定位", "位隐", "隐藏", "藏目", "目录", "与"})
	assert.NotContains(t, terms, "a", "单个字母不成词元")
	assert.NotContains(t, terms, "1")
	assert.NotContains(t, terms, "录c", "中英文交界不拼词元")
}

// 历史上的真实查询：多词、无空格的中文长查询也要召回相关条目（旧的三元组词相似度在这里召回为零）。
func TestSearch_LexicalRecallsChineseLongQuery(t *testing.T) {
	s, _ := newService(t, Models{})
	mustCreate(t, s, Draft{Title: "诱饵 flag 常自带标记词，勿当战利品提交",
		Content: "页面注释中的 flag 串若含 fake、decoy 等字样或伴随嘲讽文案，多半是诱饵。"})
	mustCreate(t, s, Draft{Title: "先建 404 基线再批量探测端点", Content: "批量探测前先记录不存在路径的响应作为基线。"})

	res, err := s.Search(context.Background(), Query{Text: "CTF 靶场 状态门户 网页 flag 区分真假 诱饵"})
	require.NoError(t, err)
	assert.Equal(t, ModeLexical, res.Mode)
	assert.Equal(t, []string{"诱饵 flag 常自带标记词，勿当战利品提交"}, titles(res.Hits))
	assert.Empty(t, res.Fallback)
}

func TestSearch_ScopeLimitsVisibility(t *testing.T) {
	s, _ := newService(t, Models{})
	mustCreate(t, s, Draft{Title: "CTF 诱饵识别", Content: "带嘲讽文案的诱饵串不提交", Scenario: ptr("ctf")})
	mustCreate(t, s, Draft{Title: "渗透 诱饵识别", Content: "蜜罐诱饵接口不要深挖", Scenario: ptr("pentest")})
	mustCreate(t, s, Draft{Title: "通用 诱饵识别", Content: "诱饵常伴随异常显眼的位置"})

	search := func(scope Scope) []string {
		res, err := s.Search(context.Background(), Query{Text: "诱饵识别", Scope: scope})
		require.NoError(t, err)
		return titles(res.Hits)
	}
	assert.ElementsMatch(t, []string{"CTF 诱饵识别", "通用 诱饵识别"}, search(Scope{Scenario: ptr("ctf")}))
	assert.ElementsMatch(t, []string{"通用 诱饵识别"}, search(Scope{}), "无场景的任务只见通用知识")
}

// 语义模式：重排分数低于门槛的候选不返回，即使它是最近邻。
func TestSearch_SemanticFiltersByRelevance(t *testing.T) {
	models, _, _ := semanticModels()
	s, _ := newService(t, models)
	mustCreate(t, s, Draft{Title: "诱饵串不提交", Content: "带 fake 字样的串是诱饵"})
	mustCreate(t, s, Draft{Title: "编码载荷先解码", Content: "标注 encoded 的载荷先解码再看结构"})

	res, err := s.Search(context.Background(), Query{Text: "怎么分辨诱饵"})
	require.NoError(t, err)
	assert.Equal(t, ModeSemantic, res.Mode)
	require.Len(t, res.Hits, 1)
	assert.Equal(t, "诱饵串不提交", res.Hits[0].Entry.Title)
	assert.InDelta(t, 0.9, res.Hits[0].Score, 1e-9)
}

// 语义检索故障时退回字面检索，并在结果里注明。
func TestSearch_FallsBackToLexicalWhenRerankFails(t *testing.T) {
	models, _, rr := semanticModels()
	s, _ := newService(t, models)
	mustCreate(t, s, Draft{Title: "诱饵串不提交", Content: "带 fake 字样的串是诱饵"})
	rr.fail.Store(true)

	res, err := s.Search(context.Background(), Query{Text: "诱饵串"})
	require.NoError(t, err)
	assert.Equal(t, ModeLexical, res.Mode)
	assert.Contains(t, res.Fallback, "重排服务不可用")
	assert.Equal(t, []string{"诱饵串不提交"}, titles(res.Hits))
}

func TestSearch_LimitIsCapped(t *testing.T) {
	s, _ := newService(t, Models{})
	for i := 0; i < MaxLimit+3; i++ {
		mustCreate(t, s, Draft{Title: "诱饵识别 " + strings.Repeat("甲", i+1), Content: "诱饵识别要点 " + strings.Repeat("乙", i+1)})
	}
	res, err := s.Search(context.Background(), Query{Text: "诱饵识别", Limit: 100})
	require.NoError(t, err)
	assert.Len(t, res.Hits, MaxLimit)
}

func TestCreate_DuplicateCarriesExistingID(t *testing.T) {
	s, _ := newService(t, Models{})
	first := mustCreate(t, s, Draft{Title: "甲", Content: "同一条知识"})

	_, err := s.Create(context.Background(), Draft{Title: "乙", Content: "  同一条知识  ", Source: SourceExpert})

	var dup *DuplicateError
	require.ErrorAs(t, err, &dup)
	assert.Equal(t, first.ID, dup.ExistingID)
	assert.ErrorIs(t, err, ErrDuplicate)
}

func TestCreate_Validates(t *testing.T) {
	s, _ := newService(t, Models{})
	for _, d := range []Draft{
		{Title: "", Content: "x", Source: SourceExpert},
		{Title: "x", Content: " ", Source: SourceExpert},
		{Title: "x", Content: strings.Repeat("长", MaxContentRunes+1), Source: SourceExpert},
		{Title: "x", Content: "x", Source: "bogus"},
	} {
		_, err := s.Create(context.Background(), d)
		assert.ErrorIs(t, err, ErrInvalid, "%+v", d)
	}
}

// 人工改了标题或正文：条目成为人工内容，向量随新文本重建；只改场景不改变作者。
func TestUpdate_TextMakesEntryAuthored(t *testing.T) {
	models, emb, _ := semanticModels()
	s, _ := newService(t, models)
	ctx := context.Background()
	e := mustCreate(t, s, Draft{Title: "诱饵", Content: "旧正文"})

	got, err := s.Update(ctx, e.ID, Patch{Scenario: ptr("ctf"), ScenarioSet: true})
	require.NoError(t, err)
	assert.Equal(t, SourceAgent, got.Source, "只改场景不改变作者")
	assert.Equal(t, "ctf", *got.Scenario)

	emb.fail.Store(true) // 新文本的向量生成失败：旧向量作废，留给补齐
	got, err = s.Update(ctx, e.ID, Patch{Content: ptr("新正文")})
	require.NoError(t, err)
	assert.Equal(t, SourceExpert, got.Source)
	assert.False(t, got.Indexed, "旧文本的向量对新文本是错的，应清空")

	got, err = s.Update(ctx, e.ID, Patch{ScenarioSet: true})
	require.NoError(t, err)
	assert.Nil(t, got.Scenario, "场景改为通用")
}

func TestRevise_RespectsAuthorAndVersion(t *testing.T) {
	s, store := newService(t, Models{})
	ctx := context.Background()
	task := seedTask(t, store)
	human := mustCreate(t, s, Draft{Title: "人工", Content: "人工正文", Source: SourceExpert})
	assert.ErrorIs(t, s.Revise(ctx, human, "改", "改写", task), ErrAuthored)

	learned := mustCreate(t, s, Draft{Title: "沉淀", Content: "沉淀正文"})
	_, err := s.Update(ctx, learned.ID, Patch{ScenarioSet: true, Scenario: ptr("ctf")})
	require.NoError(t, err)
	assert.ErrorIs(t, s.Revise(ctx, learned, "改", "改写", task), ErrStale, "其间被改过的条目不能按旧版本改写")

	fresh, _, err := s.Get(ctx, learned.ID)
	require.NoError(t, err)
	require.NoError(t, s.Revise(ctx, fresh, "改后标题", "改后正文", task))
	got, confirmations, err := s.Get(ctx, learned.ID)
	require.NoError(t, err)
	assert.Equal(t, "改后正文", got.Content)
	assert.Equal(t, SourceAgent, got.Source)
	require.Len(t, confirmations, 1)
	assert.Equal(t, task, confirmations[0].TaskID)
}

func TestConfirm_IdempotentAndCascades(t *testing.T) {
	s, store := newService(t, Models{})
	ctx := context.Background()
	task := seedTask(t, store)
	e := mustCreate(t, s, Draft{Title: "甲", Content: "乙"})

	require.NoError(t, s.Confirm(ctx, e.ID, task))
	require.NoError(t, s.Confirm(ctx, e.ID, task))
	got, _, err := s.Get(ctx, e.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, got.Confirmations, "同一任务重复印证只计一次")

	require.NoError(t, s.Delete(ctx, e.ID))
	var n int
	require.NoError(t, store.pool.QueryRow(ctx, `SELECT count(*) FROM corpus_confirmation`).Scan(&n))
	assert.Zero(t, n)
	assert.ErrorIs(t, s.Delete(ctx, e.ID), ErrNotFound)
}

func TestListAndFacets(t *testing.T) {
	s, _ := newService(t, Models{})
	ctx := context.Background()
	mustCreate(t, s, Draft{Title: "a1", Content: "诱饵一", Scenario: ptr("ctf")})
	mustCreate(t, s, Draft{Title: "a2", Content: "诱饵二", Scenario: ptr("ctf"), Source: SourceExpert})
	mustCreate(t, s, Draft{Title: "a3", Content: "基线三"})

	page, err := s.List(ctx, ListQuery{Scenario: "ctf", Limit: 10})
	require.NoError(t, err)
	assert.Equal(t, 2, page.Total)

	page, err = s.List(ctx, ListQuery{General: true, Limit: 10})
	require.NoError(t, err)
	assert.Equal(t, 1, page.Total)

	page, err = s.List(ctx, ListQuery{Search: "诱饵_", Limit: 10})
	require.NoError(t, err)
	assert.Zero(t, page.Total, "搜索词里的 _ 按字面匹配")

	f, err := s.Facets(ctx, ListQuery{Scenario: "ctf", Source: SourceExpert})
	require.NoError(t, err)
	// 场景分面不受场景筛选影响（只受来源筛选）：expert 只有 ctf 的一条
	require.Len(t, f.Scenarios, 1)
	assert.Equal(t, 1, f.Scenarios[0].Count)
	// 来源分面不受来源筛选影响（只受场景筛选）：ctf 下 agent 1、expert 1
	assert.ElementsMatch(t, []SourceFacet{{Source: SourceAgent, Count: 1}, {Source: SourceExpert, Count: 1}}, f.Sources)

	_, err = s.List(ctx, ListQuery{Scenario: "ctf", General: true})
	assert.ErrorIs(t, err, ErrInvalid)
}

// 写入时向量化失败的条目由补齐收账；补齐后进入语义召回。
func TestIndexPending_FillsMissingVectors(t *testing.T) {
	models, emb, _ := semanticModels()
	s, _ := newService(t, models)
	ctx := context.Background()
	emb.fail.Store(true)
	e := mustCreate(t, s, Draft{Title: "诱饵串不提交", Content: "带 fake 字样的串是诱饵"})
	assert.False(t, e.Indexed)

	emb.fail.Store(false)
	n, err := s.IndexPending(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	st, err := s.Status(ctx)
	require.NoError(t, err)
	assert.Equal(t, IndexStats{Total: 1, Pending: 0}, st.Index)
	assert.Equal(t, ModeSemantic, st.Mode)
}

// 写锁跨连接互斥：第二个持锁方要等第一个释放。
func TestExclusive_Serializes(t *testing.T) {
	s, _ := newService(t, Models{})
	ctx := context.Background()
	entered := make(chan struct{})
	release := make(chan struct{})
	go func() {
		_ = s.Exclusive(ctx, func(context.Context) error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered
	second := make(chan struct{})
	go func() {
		_ = s.Exclusive(ctx, func(context.Context) error { close(second); return nil })
	}()
	select {
	case <-second:
		t.Fatal("第一个持锁方未释放前，第二个不应进入")
	case <-time.After(300 * time.Millisecond):
	}
	close(release)
	select {
	case <-second:
	case <-time.After(5 * time.Second):
		t.Fatal("锁释放后第二个持锁方应进入")
	}
}

func TestImport_SplitsAndSkipsDuplicates(t *testing.T) {
	s, _ := newService(t, Models{})
	ctx := context.Background()
	md := "# 侦察\n先看 robots.txt\n\n## 基线\n先建 404 基线\n```sh\n# 这是注释不是标题\ncurl x\n```\n"

	res, err := s.Import(ctx, md, ptr("ctf"))
	require.NoError(t, err)
	assert.Equal(t, ImportResult{Imported: 2}, res)

	res, err = s.Import(ctx, md, ptr("ctf"))
	require.NoError(t, err)
	assert.Equal(t, ImportResult{Duplicates: 2}, res)

	page, err := s.List(ctx, ListQuery{Source: SourceExpert, Limit: 10})
	require.NoError(t, err)
	require.Equal(t, 2, page.Total)
}

// 合并：保留第一条并改写，其余的印证转给它后删除；同一任务的印证只记一次。
func TestMerge_MovesConfirmationsAndDeletesAbsorbed(t *testing.T) {
	s, store := newService(t, Models{})
	ctx := context.Background()
	t1, t2, t3 := seedTask(t, store), seedTask(t, store), seedTask(t, store)
	keep := mustCreate(t, s, Draft{Title: "甲", Content: "甲正文"})
	b := mustCreate(t, s, Draft{Title: "乙", Content: "乙正文"})
	require.NoError(t, s.Confirm(ctx, keep.ID, t1))
	require.NoError(t, s.Confirm(ctx, b.ID, t1))
	require.NoError(t, s.Confirm(ctx, b.ID, t2))

	keep, _, _ = s.Get(ctx, keep.ID)
	b, _, _ = s.Get(ctx, b.ID)
	require.NoError(t, s.Merge(ctx, keep, []Entry{b}, "合并后", "合并后的正文", t3))

	got, confirmations, err := s.Get(ctx, keep.ID)
	require.NoError(t, err)
	assert.Equal(t, "合并后的正文", got.Content)
	assert.Equal(t, 3, got.Confirmations, "t1 两边都有只记一次，加上 t2 与本次的 t3")
	assert.Len(t, confirmations, 3)
	_, _, err = s.Get(ctx, b.ID)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestMerge_Guards(t *testing.T) {
	s, store := newService(t, Models{})
	ctx := context.Background()
	task := seedTask(t, store)
	a := mustCreate(t, s, Draft{Title: "甲", Content: "甲正文"})
	b := mustCreate(t, s, Draft{Title: "乙", Content: "乙正文"})
	human := mustCreate(t, s, Draft{Title: "人", Content: "人工正文", Source: SourceExpert})
	scoped := mustCreate(t, s, Draft{Title: "场", Content: "场景正文", Scenario: ptr("ctf")})

	assert.ErrorIs(t, s.Merge(ctx, a, []Entry{human}, "x", "y", task), ErrAuthored)
	assert.ErrorIs(t, s.Merge(ctx, a, []Entry{scoped}, "x", "y", task), ErrInvalid, "适用范围不同不能合并")
	assert.ErrorIs(t, s.Merge(ctx, a, nil, "x", "y", task), ErrInvalid)

	_, err := s.Update(ctx, b.ID, Patch{ScenarioSet: true}) // 其间被改过
	require.NoError(t, err)
	assert.ErrorIs(t, s.Merge(ctx, a, []Entry{b}, "x", "y", task), ErrStale)
	_, _, err = s.Get(ctx, b.ID)
	assert.NoError(t, err, "合并失败不删任何条目")
}

// 多词元的查询只命中一个词元（往往是常见词）不算相关：字面检索至少要命中两个查询词元。
func TestSearch_LexicalNeedsTwoMatchedTerms(t *testing.T) {
	s, _ := newService(t, Models{})
	mustCreate(t, s, Draft{Title: "显眼位置的 flag 串先当诱饵再验证", Content: "伴随嘲讽文案的串多半是诱饵，真假要靠接口行为分辨。"})
	mustCreate(t, s, Draft{Title: "flag 回报前留档原始证据", Content: "保存出处响应原文。"})
	mustCreate(t, s, Draft{Title: "运维笔记列出未公开端点", Content: "逐条实测笔记里的 flag 线索。"})
	for i, topic := range []string{"目录索引", "接口探测", "响应基线", "编码解析", "请求方法", "参数开关", "备份文件"} {
		mustCreate(t, s, Draft{Title: topic + "要点", Content: fmt.Sprintf("%s 的第 %d 条笔记。", topic, i)})
	}

	res, err := s.Search(context.Background(), Query{Text: "怎么分辨真假 flag"})
	require.NoError(t, err)
	assert.Equal(t, []string{"显眼位置的 flag 串先当诱饵再验证"}, titles(res.Hits))

	res, err = s.Search(context.Background(), Query{Text: "诱饵"})
	require.NoError(t, err)
	assert.Equal(t, []string{"显眼位置的 flag 串先当诱饵再验证"}, titles(res.Hits), "单词元查询命中一个即可")
}
