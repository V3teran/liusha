package corpus

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	"github.com/jackc/pgx/v5"
)

// Mode 是一次检索实际采用的方式。
type Mode string

const (
	ModeSemantic Mode = "semantic" // 向量与词元召回 + 重排
	ModeLexical  Mode = "lexical"  // 只按词元匹配
)

// 检索参数。两个门槛用库内真实条目与历史上的真实查询（多为一串关键词）标定：
//   - 重排相关度：相关条目 0.30~0.62，无关条目多在 0.30 以下、个别到 0.4 左右，两者有重叠。
//     多返回一条无关条目只占用一点上下文、由读者判断取舍；漏掉相关条目则知识白存，门槛取重叠区下沿。
//   - 字面覆盖率：多词查询里的情境词（库里没有）把覆盖率整体压低，相关条目 0.067~0.54、无关条目
//     0.037~0.13，重叠更大；门槛只切掉只命中一个常见词的最弱匹配。
const (
	DefaultLimit = 5
	MaxLimit     = 10
	// recallSize 是每一路召回的候选数（之后统一重排或打分）。
	recallSize = 20
	// minRelevance 是重排相关度门槛（语义模式）。
	minRelevance = 0.3
	// minCoverage 是 IDF 加权的查询词覆盖率门槛（字面模式）。
	minCoverage = 0.05
)

// Scope 是检索的可见范围：任务看得到本场景与通用的知识；Scenario 为 nil 的任务只看得到通用知识。
type Scope struct{ Scenario *string }

// Query 是一次检索。
type Query struct {
	Text  string
	Scope Scope
	Limit int // 0 = DefaultLimit，超过 MaxLimit 按 MaxLimit
}

// Hit 是一条命中：条目与它的得分（语义模式为重排相关度，字面模式为加权覆盖率，均在 0~1）。
type Hit struct {
	Entry Entry
	Score float64
}

// Result 是检索结果。Fallback 非空表示语义检索出了故障、本次退回字面检索，值为故障原因。
type Result struct {
	Mode     Mode
	Fallback string
	Hits     []Hit
}

func (q Query) limit() int {
	switch {
	case q.Limit <= 0:
		return DefaultLimit
	case q.Limit > MaxLimit:
		return MaxLimit
	}
	return q.Limit
}

// scopeCond 拼出可见范围条件，参数位从 next 开始。
func scopeCond(s Scope, next int) (string, []any) {
	if s.Scenario == nil {
		return "c.scenario IS NULL", nil
	}
	return "(c.scenario = $" + strconv.Itoa(next) + " OR c.scenario IS NULL)", []any{*s.Scenario}
}

// recallDense 取向量最近邻。按场景过滤时 HNSW 先取近邻再过滤，会凑不够数；开启迭代扫描
// 让索引在过滤后继续往下找（宽松顺序，外层再按距离排一次）。
func (s *Store) recallDense(ctx context.Context, vec []float32, scope Scope, n int) ([]Entry, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("向量召回: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET LOCAL hnsw.iterative_scan = relaxed_order`); err != nil {
		return nil, fmt.Errorf("向量召回: %w", err)
	}
	cond, args := scopeCond(scope, 3)
	rows, err := tx.Query(ctx, `
		WITH near AS MATERIALIZED (
			SELECT c.id, c.embedding <=> $1 AS distance FROM corpus c
			WHERE c.embedding IS NOT NULL AND `+cond+`
			ORDER BY c.embedding <=> $1 LIMIT $2
		)
		SELECT `+entryCols+` FROM near JOIN corpus c ON c.id = near.id ORDER BY near.distance`,
		append([]any{vectorArg(vec), n}, args...)...)
	if err != nil {
		return nil, fmt.Errorf("向量召回: %w", err)
	}
	entries, err := collectEntries(rows)
	if err != nil {
		return nil, err
	}
	return entries, tx.Commit(ctx)
}

// recallLexical 按词元召回并打分：得分是命中的查询词元的 IDF 之和占全部查询词元 IDF 之和的比例。
// 库里没有的查询词按最高权重计入分母——它们最能说明条目与查询无关；只计出现过的词会让只命中一个
// 常见词的条目拿到满分。IDF 在可见范围内计算。
func (s *Store) recallLexical(ctx context.Context, text string, scope Scope, n int) ([]Hit, error) {
	cond, args := scopeCond(scope, 3)
	rows, err := s.pool.Query(ctx, `
		WITH q AS (SELECT corpus_terms($1) AS terms),
		visible AS (SELECT c.* FROM corpus c WHERE `+cond+`),
		df AS (
			SELECT t, (SELECT count(*) FROM visible v WHERE t = ANY (v.terms)) AS n
			FROM q, unnest(q.terms) t
		),
		w AS (
			SELECT df.t, ln(1 + ((SELECT count(*) FROM visible) - df.n + 0.5) / (df.n + 0.5)) AS idf FROM df
		),
		scored AS (
			SELECT c.id,
				(SELECT sum(w.idf) FROM w WHERE w.t = ANY (c.terms)) / NULLIF((SELECT sum(idf) FROM w), 0) AS score
			FROM visible c, q WHERE c.terms && q.terms
		)
		SELECT `+entryCols+`, scored.score FROM scored JOIN corpus c ON c.id = scored.id
		WHERE scored.score IS NOT NULL
		ORDER BY scored.score DESC, c.updated_at DESC LIMIT $2`,
		append([]any{text, n}, args...)...)
	if err != nil {
		return nil, fmt.Errorf("词元召回: %w", err)
	}
	return collectHits(rows)
}

func collectHits(rows pgx.Rows) ([]Hit, error) {
	defer rows.Close()
	var out []Hit
	for rows.Next() {
		var h Hit
		e := &h.Entry
		if err := rows.Scan(&e.ID, &e.Title, &e.Content, &e.Scenario, &e.Source, &e.Indexed,
			&e.Confirmations, &e.CreatedAt, &e.UpdatedAt, &h.Score); err != nil {
			return nil, fmt.Errorf("读取召回结果: %w", err)
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历召回结果: %w", err)
	}
	return out, nil
}

// searchLexical 是字面检索：词元召回，按覆盖率门槛过滤。
func (s *Store) searchLexical(ctx context.Context, q Query) ([]Hit, error) {
	hits, err := s.recallLexical(ctx, q.Text, q.Scope, recallSize)
	if err != nil {
		return nil, err
	}
	return keep(hits, minCoverage, q.limit()), nil
}

// searchSemantic 是语义检索：向量与词元两路召回，合并去重后统一重排，按相关度门槛过滤。
func (s *Store) searchSemantic(ctx context.Context, m Models, q Query) ([]Hit, error) {
	vec, err := m.Embedder.EmbedQuery(ctx, q.Text)
	if err != nil {
		return nil, fmt.Errorf("查询向量化: %w", err)
	}
	dense, err := s.recallDense(ctx, vec, q.Scope, recallSize)
	if err != nil {
		return nil, err
	}
	lexical, err := s.recallLexical(ctx, q.Text, q.Scope, recallSize)
	if err != nil {
		return nil, err
	}
	candidates := make([]Entry, 0, len(dense)+len(lexical))
	seen := make(map[string]bool, cap(candidates))
	for _, e := range dense {
		seen[e.ID] = true
		candidates = append(candidates, e)
	}
	for _, h := range lexical {
		if !seen[h.Entry.ID] {
			seen[h.Entry.ID] = true
			candidates = append(candidates, h.Entry)
		}
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	docs := make([]string, len(candidates))
	for i, e := range candidates {
		docs[i] = e.Text()
	}
	ranked, err := m.Reranker.Rerank(ctx, q.Text, docs, len(docs))
	if err != nil {
		return nil, fmt.Errorf("重排: %w", err)
	}
	hits := make([]Hit, 0, len(ranked))
	for _, r := range ranked {
		if r.Index >= 0 && r.Index < len(candidates) {
			hits = append(hits, Hit{Entry: candidates[r.Index], Score: r.Score})
		}
	}
	return keep(hits, minRelevance, q.limit()), nil
}

// keep 按得分降序取门槛以上的前 limit 条。
func keep(hits []Hit, threshold float64, limit int) []Hit {
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	out := make([]Hit, 0, limit)
	for _, h := range hits {
		if h.Score < threshold || len(out) == limit {
			break
		}
		out = append(out, h)
	}
	return out
}
