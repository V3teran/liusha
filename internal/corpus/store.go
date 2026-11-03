package corpus

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"
)

// Store 封装 corpus 表的持久化 + hybrid 检索。
type Store struct{ pool *pgxpool.Pool }

// NewStore 用 pgxpool 构造。
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Add 写一条知识。按 content_hash 去重（重复写视为再次确认：hit_count++、刷新 title/tags/embedding）。
//
// embedding 可空（Jina 不可用时降级，只落行）。source 必填 agent|expert。
func (s *Store) Add(ctx context.Context, e Entry) (Entry, error) {
	if e.Content == "" {
		return Entry{}, fmt.Errorf("corpus.Add: content 必填")
	}
	if e.Source != SourceAgent && e.Source != SourceExpert {
		return Entry{}, fmt.Errorf("corpus.Add: source 非法 %q（agent|expert）", e.Source)
	}
	e.ContentHash = ContentHash(e.Content)
	if e.Tags == nil {
		e.Tags = []string{}
	}

	// 向量：非空传 pgvector.Vector（driver.Valuer 编码），空传 NULL。
	var embArg any
	if len(e.Embedding) > 0 {
		embArg = pgvector.NewVector(e.Embedding)
	}
	var taskArg any
	if e.SourceTaskID != "" {
		taskArg = e.SourceTaskID
	}

	row := s.pool.QueryRow(ctx, `
		INSERT INTO corpus (title, content, tags, source, source_task_id, embedding, content_hash)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (content_hash) DO UPDATE
		  SET title      = EXCLUDED.title,
		      tags       = EXCLUDED.tags,
		      embedding  = COALESCE(EXCLUDED.embedding, corpus.embedding),
		      hit_count  = corpus.hit_count + 1,
		      updated_at = now()
		RETURNING id, hit_count, created_at, updated_at`,
		e.Title, e.Content, e.Tags, e.Source, taskArg, embArg, e.ContentHash)

	if err := row.Scan(&e.ID, &e.HitCount, &e.CreatedAt, &e.UpdatedAt); err != nil {
		return Entry{}, fmt.Errorf("save corpus: %w", err)
	}
	return e, nil
}

// selectCols 是检索回传列（不含 embedding——向量不需要回给调用方）。
const selectCols = "id, title, content, tags, source, hit_count, created_at, updated_at"

// recallDense 用 pgvector HNSW cosine 召回 top-n 最近邻。queryVec 为空则跳过（返回 nil）。
// tags 非空时先 metadata 硬过滤。
func (s *Store) recallDense(ctx context.Context, queryVec []float32, tags []string, n int) ([]Entry, error) {
	if len(queryVec) == 0 {
		return nil, nil // 无 query 向量（Jina 不可用），dense 路跳过
	}
	sql := `SELECT ` + selectCols + ` FROM corpus WHERE embedding IS NOT NULL`
	args := []any{pgvector.NewVector(queryVec)}
	if len(tags) > 0 {
		sql += ` AND tags && $2`
		args = append(args, tags)
	}
	sql += ` ORDER BY embedding <=> $1 LIMIT ` + strconv.Itoa(n)
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("corpus dense recall: %w", err)
	}
	defer rows.Close()
	return scanRows(rows)
}

// recallSparse 用 pg_trgm 相似度召回 top-n（content 与 title 取较大相似度）。
// tags 非空时先 metadata 硬过滤。
func (s *Store) recallSparse(ctx context.Context, query string, tags []string, n int) ([]Entry, error) {
	sql := `SELECT ` + selectCols + ` FROM corpus
		WHERE (content %` + `> $1 OR title %` + `> $1)`
	args := []any{query}
	if len(tags) > 0 {
		sql += ` AND tags && $2`
		args = append(args, tags)
	}
	sql += ` ORDER BY GREATEST(similarity(content,$1), similarity(title,$1)) DESC LIMIT ` + strconv.Itoa(n)
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("corpus sparse recall: %w", err)
	}
	defer rows.Close()
	return scanRows(rows)
}

// SearchHybrid 是 hybrid 检索入口：dense + sparse 两路各召回 recallN → 按 id 合并去重 → 交给
// reranker 精排取 topK。queryVec 为空则退纯 sparse；reranker 为空/失败则以效用计数兜底排序
// （hit_count 降序，稳定序保持 dense 先于 sparse 的召回相关性），不阻塞。
//
// 实际返回（被消费）的条目效用计数 +1——检索消费是效用信号，沉淀给兜底排序
// 与未来的淘汰策略；这是 hit_count 的读路径闭环（写路径的重复确认已在 Add）。
//
// dense 召回需调用方先 embed 出 queryVec（本包不依赖 embedding 包，保持解耦、可测）。
func (s *Store) SearchHybrid(ctx context.Context, query string, queryVec []float32, tags []string, recallN, topK int, rr Reranker) ([]Entry, error) {
	if recallN <= 0 {
		recallN = 20
	}
	if topK <= 0 {
		topK = 5
	}

	dense, err := s.recallDense(ctx, queryVec, tags, recallN)
	if err != nil {
		return nil, err
	}
	sparse, err := s.recallSparse(ctx, query, tags, recallN)
	if err != nil {
		return nil, err
	}

	// 按 id 合并去重（dense 优先——语义命中通常更相关，作为 rerank 失败时的兜底顺序）。
	merged := make([]Entry, 0, len(dense)+len(sparse))
	seen := make(map[string]struct{}, len(dense)+len(sparse))
	for _, e := range append(dense, sparse...) {
		if _, dup := seen[e.ID]; dup {
			continue
		}
		seen[e.ID] = struct{}{}
		merged = append(merged, e)
	}
	if len(merged) == 0 {
		return nil, nil
	}

	// 无 reranker / rerank 失败 → 效用计数兜底排序（稳定序保召回相关性）。
	if rr == nil {
		return s.consume(ctx, sortByUtility(merged), topK), nil
	}
	docs := make([]string, len(merged))
	for i, e := range merged {
		docs[i] = e.Content
	}
	order, err := rr.Rerank(ctx, query, docs, topK)
	if err != nil || len(order) == 0 {
		// rerank 失败降级：不阻塞检索，走效用兜底。
		return s.consume(ctx, sortByUtility(merged), topK), nil
	}
	ranked := make([]Entry, 0, len(order))
	for _, idx := range order {
		if idx >= 0 && idx < len(merged) {
			ranked = append(ranked, merged[idx])
		}
	}
	return s.consume(ctx, ranked, topK), nil
}

// sortByUtility 以效用计数为主序的稳定排序：hit_count 高者先（沉淀越多的
// 教训越靠前）；同计数保持原合并序（dense 召回先于 sparse——语义相关性优先）。
// rerank 在场时精排全权，本排序只服务于兜底路径。
func sortByUtility(entries []Entry) []Entry {
	sorted := make([]Entry, len(entries))
	copy(sorted, entries)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].HitCount > sorted[j].HitCount
	})
	return sorted
}

// consume 截 topK 并把实际返回条目的效用计数 +1（best-effort：软信号记账，
// 失败不影响检索交付，也不承担审计义务）。
func (s *Store) consume(ctx context.Context, entries []Entry, topK int) []Entry {
	out := capEntries(entries, topK)
	ids := make([]string, 0, len(out))
	for _, e := range out {
		ids = append(ids, e.ID)
	}
	// id 列侧转 text：pgx 把 []string 编码为 text[]，uuid = text 无算子会让
	// 本更新静默报错（错误被吞、计数器永不增长）。转型写在列侧，参数恒为
	// text[]，对驱动编解码行为零依赖；corpus 是小表，转型扫描成本可忽略。
	_, _ = s.pool.Exec(ctx,
		"UPDATE corpus SET hit_count = hit_count + 1 WHERE id::text = ANY($1)", ids)
	return out
}

// Reranker 与 embedding.Reranker 同形（此处窄声明，避免 corpus 依赖 embedding 包）。
type Reranker interface {
	Rerank(ctx context.Context, query string, docs []string, topK int) ([]int, error)
}

func capEntries(es []Entry, k int) []Entry {
	if len(es) > k {
		return es[:k]
	}
	return es
}

// scanRows 反序列化 SELECT 出的 corpus 行（不取 embedding，检索不需要回传向量）。
func scanRows(rows pgx.Rows) ([]Entry, error) {
	var out []Entry
	for rows.Next() {
		var e Entry
		if err := rows.Scan(
			&e.ID, &e.Title, &e.Content, &e.Tags, &e.Source,
			&e.HitCount, &e.CreatedAt, &e.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan corpus row: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate corpus rows: %w", err)
	}
	return out, nil
}
