package corpus

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/rs/zerolog"
)

// Service 是知识库的唯一入口：检索、增删改、印证与向量补齐。
type Service struct {
	store  *Store
	models Models
	logger zerolog.Logger
}

// NewService 构造知识库服务。models 不具备语义检索能力时只做字面检索、写入不生成向量。
func NewService(store *Store, models Models, logger zerolog.Logger) *Service {
	return &Service{store: store, models: models, logger: logger.With().Str("component", "corpus").Logger()}
}

// Mode 是配置决定的检索方式（运行期故障另见 Result.Fallback）。
func (s *Service) Mode() Mode {
	if s.models.Semantic() {
		return ModeSemantic
	}
	return ModeLexical
}

// Status 是知识库的检索能力与索引覆盖情况。
type Status struct {
	Mode           Mode
	EmbeddingModel string // 语义模式下启用的向量模型
	RerankModel    string // 语义模式下启用的重排模型
	Index          IndexStats
}

// Status 返回检索能力与索引覆盖情况。
func (s *Service) Status(ctx context.Context) (Status, error) {
	st, err := s.store.indexStats(ctx)
	if err != nil {
		return Status{}, err
	}
	out := Status{Mode: s.Mode(), Index: st}
	if s.models.Semantic() {
		out.EmbeddingModel, out.RerankModel = s.models.EmbeddingModel, s.models.RerankModel
	}
	return out, nil
}

// Search 检索给读者用的知识：按相关度门槛过滤，宁可返回空。语义检索出故障时退回字面检索并在
// 结果里注明（调用方取消除外）。
func (s *Service) Search(ctx context.Context, q Query) (Result, error) {
	res, err := s.rank(ctx, q.Text, q.Scope)
	if err != nil {
		return Result{}, err
	}
	threshold := minRelevance
	if res.Mode == ModeLexical {
		threshold = minCoverage
	}
	res.Hits = keep(res.Hits, threshold, q.limit())
	return res, nil
}

// Neighbors 取与 text 最接近的 k 条已有知识，不设相关度门槛：供收尾整合判断候选经验是新增、
// 印证还是合并。两种用途的代价相反——给读者的检索多一条无关条目会占上下文、误导判断，要精确；
// 整合多给一条近邻只是让判定多看一眼，漏掉一条重复就会重复写入，要召回。标定显示以经验全文
// 为查询时，重复条目与其余条目的分数重叠很大（重复条目最低 0.21），却几乎都排在前 3 名。
func (s *Service) Neighbors(ctx context.Context, text string, scope Scope, k int) ([]Entry, error) {
	res, err := s.rank(ctx, text, scope)
	if err != nil {
		return nil, err
	}
	hits := keep(res.Hits, 0, k)
	out := make([]Entry, len(hits))
	for i, h := range hits {
		out[i] = h.Entry
	}
	return out, nil
}

// rank 按配置的检索方式给可见范围内的候选排序（全部候选，不过滤）。
func (s *Service) rank(ctx context.Context, text string, scope Scope) (Result, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return Result{}, fmt.Errorf("%w：检索内容不能为空", ErrInvalid)
	}
	if s.models.Semantic() {
		hits, err := s.store.rankSemantic(ctx, s.models, text, scope)
		if err == nil {
			return Result{Mode: ModeSemantic, Hits: hits}, nil
		}
		if ctx.Err() != nil {
			return Result{}, err
		}
		s.logger.Warn().Err(err).Msg("语义检索失败，本次退回字面检索")
		hits, lexErr := s.store.recallLexical(ctx, text, scope, recallSize)
		if lexErr != nil {
			return Result{}, errors.Join(err, lexErr)
		}
		return Result{Mode: ModeLexical, Fallback: err.Error(), Hits: hits}, nil
	}
	hits, err := s.store.recallLexical(ctx, text, scope, recallSize)
	if err != nil {
		return Result{}, err
	}
	return Result{Mode: ModeLexical, Hits: hits}, nil
}

// embed 为条目文本生成向量；未启用语义检索返回 nil。失败只记日志、返回 nil——条目照常写入，
// 向量由 IndexPending 补齐。
func (s *Service) embed(ctx context.Context, text string) []float32 {
	if !s.models.Semantic() {
		return nil
	}
	vecs, err := s.models.Embedder.EmbedPassage(ctx, []string{text})
	if err != nil || len(vecs) != 1 {
		s.logger.Warn().Err(err).Msg("知识条目向量化失败，先写入、稍后补齐")
		return nil
	}
	return vecs[0]
}

// Create 写入一条新知识。正文已存在返回 ErrDuplicate。
func (s *Service) Create(ctx context.Context, d Draft) (Entry, error) {
	d, err := d.Normalize()
	if err != nil {
		return Entry{}, err
	}
	return s.store.insert(ctx, d, s.embed(ctx, Text(d.Title, d.Content)))
}

// Get 读取一条知识与印证过它的任务。
func (s *Service) Get(ctx context.Context, id string) (Entry, []Confirmation, error) {
	e, err := s.store.get(ctx, id)
	if err != nil {
		return Entry{}, nil, err
	}
	cs, err := s.store.confirmations(ctx, id)
	if err != nil {
		return Entry{}, nil, err
	}
	return e, cs, nil
}

// Page 是一页列表。
type Page struct {
	Items []Entry
	Total int
}

// List 列出知识（按更新时间倒序）。
func (s *Service) List(ctx context.Context, q ListQuery) (Page, error) {
	if err := q.Validate(); err != nil {
		return Page{}, err
	}
	items, total, err := s.store.list(ctx, q)
	if err != nil {
		return Page{}, err
	}
	return Page{Items: items, Total: total}, nil
}

// Facets 返回列表筛选项的分面计数。
func (s *Service) Facets(ctx context.Context, q ListQuery) (Facets, error) {
	if err := q.Validate(); err != nil {
		return Facets{}, err
	}
	return s.store.facets(ctx, q)
}

// Patch 是人工对条目的修改；nil 字段不改。ScenarioSet 为真时把场景改为 Scenario（nil = 通用）。
type Patch struct {
	Title       *string
	Content     *string
	Scenario    *string
	ScenarioSet bool
}

// Update 应用人工修改。改了标题或正文，条目即为人工内容（source = expert），此后收尾整合
// 只能印证、不再改写它；只改场景不改变作者。
func (s *Service) Update(ctx context.Context, id string, p Patch) (Entry, error) {
	cur, err := s.store.get(ctx, id)
	if err != nil {
		return Entry{}, err
	}
	f := fields{Title: cur.Title, Content: cur.Content, Scenario: cur.Scenario, Source: cur.Source}
	if p.Title != nil {
		f.Title = strings.TrimSpace(*p.Title)
	}
	if p.Content != nil {
		f.Content = strings.TrimSpace(*p.Content)
	}
	if p.ScenarioSet {
		f.Scenario = p.Scenario
		if f.Scenario != nil && strings.TrimSpace(*f.Scenario) == "" {
			f.Scenario = nil
		}
	}
	if err := validate(f.Title, f.Content); err != nil {
		return Entry{}, err
	}
	textChanged := f.Title != cur.Title || f.Content != cur.Content
	var vec []float32
	if textChanged {
		f.Source = SourceExpert
		vec = s.embed(ctx, Text(f.Title, f.Content))
	}
	if err := s.store.updateIf(ctx, id, cur.UpdatedAt, f, textChanged, vec); err != nil {
		return Entry{}, err
	}
	return s.store.get(ctx, id)
}

// ErrAuthored 表示条目是人工内容，不能被收尾整合改写。
var ErrAuthored = errors.New("人工内容不能被收尾整合改写")

// Revise 用一次收尾整合的结论改写任务沉淀的条目，并记下 taskID 的印证。seen 是整合时看到的
// 版本：其间被人改过返回 ErrStale，人工内容返回 ErrAuthored——两者都由调用方另行处理。
func (s *Service) Revise(ctx context.Context, seen Entry, title, content, taskID string) error {
	if seen.Source != SourceAgent {
		return ErrAuthored
	}
	title, content = strings.TrimSpace(title), strings.TrimSpace(content)
	if err := validate(title, content); err != nil {
		return err
	}
	f := fields{Title: title, Content: content, Scenario: seen.Scenario, Source: SourceAgent}
	if err := s.store.updateIf(ctx, seen.ID, seen.UpdatedAt, f, true, s.embed(ctx, Text(title, content))); err != nil {
		return err
	}
	return s.store.confirm(ctx, seen.ID, taskID)
}

func sameScenario(a, b *string) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

// Merge 用一次收尾整合的结论把讲同一件事的若干条任务沉淀合成一条：keep 改写为新文本，
// absorbed 的印证转给 keep 后删除，并记下 taskID 的印证。条目都是整合时看到的版本：其中有
// 人工内容返回 ErrAuthored，其间被改过或删除返回 ErrStale；适用范围不同的条目不能合并（合并会
// 悄悄改变可见范围），返回 ErrInvalid。
func (s *Service) Merge(ctx context.Context, keep Entry, absorbed []Entry, title, content, taskID string) error {
	if len(absorbed) == 0 {
		return fmt.Errorf("%w：合并至少需要两条条目", ErrInvalid)
	}
	for _, e := range append([]Entry{keep}, absorbed...) {
		if e.Source != SourceAgent {
			return ErrAuthored
		}
		if !sameScenario(e.Scenario, keep.Scenario) {
			return fmt.Errorf("%w：只能合并适用范围相同的条目", ErrInvalid)
		}
	}
	title, content = strings.TrimSpace(title), strings.TrimSpace(content)
	if err := validate(title, content); err != nil {
		return err
	}
	f := fields{Title: title, Content: content, Scenario: keep.Scenario, Source: SourceAgent}
	return s.store.merge(ctx, keep, absorbed, f, s.embed(ctx, Text(title, content)), taskID)
}

// Exclusive 在知识库写锁下执行 fn（跨进程互斥）。收尾整合「检索近似条目 → 判定 → 写入」
// 必须整体在锁内，否则并发的两次整合会各自判定为新增、写出重复知识。
func (s *Service) Exclusive(ctx context.Context, fn func(ctx context.Context) error) error {
	return s.store.exclusive(ctx, fn)
}

// Confirm 记下 taskID 印证了条目（幂等）。
func (s *Service) Confirm(ctx context.Context, entryID, taskID string) error {
	return s.store.confirm(ctx, entryID, taskID)
}

// Delete 删除一条知识（连同印证记录）。
func (s *Service) Delete(ctx context.Context, id string) error {
	return s.store.delete(ctx, id)
}

// indexBatch 是一次补齐的条目数（一次向量化请求）。
const indexBatch = 32

// IndexPending 为尚无向量的条目补齐向量，返回补齐的条数。未启用语义检索时什么都不做。
// 条目在补齐期间被改写过的跳过（新文本的向量由改写方生成或下一轮补齐）。
func (s *Service) IndexPending(ctx context.Context) (int, error) {
	if !s.models.Semantic() {
		return 0, nil
	}
	done := 0
	for {
		batch, err := s.store.pendingIndex(ctx, indexBatch)
		if err != nil || len(batch) == 0 {
			return done, err
		}
		texts := make([]string, len(batch))
		for i, e := range batch {
			texts[i] = e.Text()
		}
		vecs, err := s.models.Embedder.EmbedPassage(ctx, texts)
		if err != nil {
			return done, fmt.Errorf("补齐向量: %w", err)
		}
		if len(vecs) != len(batch) {
			return done, fmt.Errorf("补齐向量: 期望 %d 个向量，得到 %d", len(batch), len(vecs))
		}
		progressed := false
		for i, e := range batch {
			ok, err := s.store.setEmbedding(ctx, e.ID, e.UpdatedAt, vecs[i])
			if err != nil {
				return done, err
			}
			if ok {
				done++
				progressed = true
			}
		}
		if !progressed {
			return done, nil // 整批都在补齐期间被改写：留给下一轮，避免空转
		}
	}
}

// ImportResult 是一次 Markdown 导入的结果。
type ImportResult struct {
	Imported   int // 新写入的条目
	Duplicates int // 正文与已有条目相同、跳过的条目
}

// Import 把 Markdown 切成条目（见 SplitMarkdown）作为人工内容写入，归属 scenario（nil = 通用）。
// 正文与已有条目相同的跳过并计数；其余写入失败即返回（已写入的保留）。
func (s *Service) Import(ctx context.Context, md string, scenario *string) (ImportResult, error) {
	var out ImportResult
	for _, sec := range SplitMarkdown(md) {
		_, err := s.Create(ctx, Draft{Title: sec.Title, Content: sec.Content, Scenario: scenario, Source: SourceExpert})
		switch {
		case errors.Is(err, ErrDuplicate):
			out.Duplicates++
		case err != nil:
			return out, fmt.Errorf("导入「%s」: %w", sec.Title, err)
		default:
			out.Imported++
		}
	}
	return out, nil
}
