package corpus

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"

	"github.com/V3teran/liusha/internal/db"
)

// ErrStale 表示条目在读取之后被改过（乐观锁前置不成立）。
var ErrStale = errors.New("知识条目已被修改，请刷新后重试")

// Store 是 corpus 表与印证表的持久化。
type Store struct{ pool *pgxpool.Pool }

// NewStore 用连接池构造。
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// entryCols 是读取条目的列（与 scanEntry 一一对应；c 是 corpus 的别名）。
const entryCols = `c.id, c.title, c.content, c.scenario, c.source, c.embedding IS NOT NULL,
	(SELECT count(*) FROM corpus_confirmation f WHERE f.entry_id = c.id), c.created_at, c.updated_at`

func scanEntry(row pgx.Row) (Entry, error) {
	var e Entry
	err := row.Scan(&e.ID, &e.Title, &e.Content, &e.Scenario, &e.Source, &e.Indexed,
		&e.Confirmations, &e.CreatedAt, &e.UpdatedAt)
	return e, err
}

func collectEntries(rows pgx.Rows) ([]Entry, error) {
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, fmt.Errorf("读取知识条目: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历知识条目: %w", err)
	}
	return out, nil
}

func vectorArg(v []float32) any {
	if len(v) == 0 {
		return nil
	}
	return pgvector.NewVector(v)
}

// isDuplicate 判断是否撞上正文唯一约束。
func isDuplicate(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "corpus_content_hash_uniq"
}

// mapWriteErr 把正文唯一约束冲突收敛为带出已有条目的 *DuplicateError。
func (s *Store) mapWriteErr(ctx context.Context, err error, content string) error {
	if !isDuplicate(err) {
		return err
	}
	var id string
	if qErr := s.pool.QueryRow(ctx, `SELECT id FROM corpus WHERE content_hash = $1`, contentHash(content)).Scan(&id); qErr != nil {
		return fmt.Errorf("%w（读取已有条目失败: %w）", ErrDuplicate, qErr)
	}
	return &DuplicateError{ExistingID: id}
}

// writeLockKey 是知识库写锁的 advisory lock 键（任意固定值，只需全库唯一）。
const writeLockKey int64 = 0x636f72707573 // "corpus"

// exclusive 在知识库写锁下执行 fn：跨进程互斥，用于「先查近似、再决定写什么」这类读后写，
// 避免并发的两个写入方都没看见对方、各写一条重复知识。锁随连接持有，fn 返回即释放。
func (s *Store) exclusive(ctx context.Context, fn func(ctx context.Context) error) error {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("获取知识库写锁: %w", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, writeLockKey); err != nil {
		return fmt.Errorf("获取知识库写锁: %w", err)
	}
	defer func() {
		// 用独立上下文释放：fn 超时或被取消时仍要放锁（连接回池前必须解锁）。
		uctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if _, err := conn.Exec(uctx, `SELECT pg_advisory_unlock($1)`, writeLockKey); err != nil {
			_ = conn.Conn().Close(uctx) // 解锁失败就关掉连接，会话结束锁随之释放；关闭本身的失败无可再做
		}
	}()
	return fn(ctx)
}

// insert 写入一条新知识（embedding 可空）。正文已存在返回 ErrDuplicate。
func (s *Store) insert(ctx context.Context, d Draft, embedding []float32) (Entry, error) {
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO corpus (title, content, scenario, source, embedding, content_hash)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id`,
		d.Title, d.Content, d.Scenario, d.Source, vectorArg(embedding), contentHash(d.Content)).Scan(&id)
	if err != nil {
		return Entry{}, fmt.Errorf("写入知识条目: %w", s.mapWriteErr(ctx, err, d.Content))
	}
	return s.get(ctx, id)
}

func (s *Store) get(ctx context.Context, id string) (Entry, error) {
	e, err := scanEntry(s.pool.QueryRow(ctx, `SELECT `+entryCols+` FROM corpus c WHERE c.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Entry{}, fmt.Errorf("%w: %w", ErrNotFound, err)
	}
	if err != nil {
		return Entry{}, fmt.Errorf("读取知识条目 %s: %w", id, err)
	}
	return e, nil
}

// confirmations 列出印证过条目的任务（新的在前）。
func (s *Store) confirmations(ctx context.Context, id string) ([]Confirmation, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT f.task_id, t.goal, f.confirmed_at
		FROM corpus_confirmation f JOIN task t ON t.id = f.task_id
		WHERE f.entry_id = $1
		ORDER BY f.confirmed_at DESC`, id)
	if err != nil {
		return nil, fmt.Errorf("读取印证记录: %w", err)
	}
	defer rows.Close()
	out := []Confirmation{}
	for rows.Next() {
		var c Confirmation
		if err := rows.Scan(&c.TaskID, &c.TaskGoal, &c.ConfirmedAt); err != nil {
			return nil, fmt.Errorf("读取印证记录: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// fields 是一次改写落定的条目字段。
type fields struct {
	Title    string
	Content  string
	Scenario *string
	Source   string
}

// updateIf 在条目仍是 seen 版本时改写它。textChanged 时一并替换向量（embedding 为空即清空，
// 留给 IndexPending 补齐——旧文本的向量对新文本是错的，宁可暂缺）。
func (s *Store) updateIf(ctx context.Context, id string, seen time.Time, f fields, textChanged bool, embedding []float32) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE corpus SET title = $3, content = $4, scenario = $5, source = $6,
			content_hash = $7,
			embedding = CASE WHEN $8 THEN $9::vector ELSE embedding END,
			updated_at = now()
		WHERE id = $1 AND updated_at = $2`,
		id, seen, f.Title, f.Content, f.Scenario, f.Source, contentHash(f.Content), textChanged, vectorArg(embedding))
	if err != nil {
		return fmt.Errorf("改写知识条目: %w", s.mapWriteErr(ctx, err, f.Content))
	}
	if tag.RowsAffected() == 0 {
		if _, err := s.get(ctx, id); err != nil {
			return err
		}
		return ErrStale
	}
	return nil
}

func (s *Store) delete(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM corpus WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("删除知识条目: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: %w", ErrNotFound, pgx.ErrNoRows)
	}
	return nil
}

// confirm 记下 taskID 印证了条目（幂等）。
func (s *Store) confirm(ctx context.Context, entryID, taskID string) error {
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO corpus_confirmation (entry_id, task_id) VALUES ($1, $2)
		ON CONFLICT DO NOTHING`, entryID, taskID); err != nil {
		return fmt.Errorf("记录印证: %w", err)
	}
	return nil
}

// pendingIndex 取尚无向量的条目（旧的先补）。
func (s *Store) pendingIndex(ctx context.Context, limit int) ([]Entry, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+entryCols+` FROM corpus c
		WHERE c.embedding IS NULL ORDER BY c.updated_at LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("读取待补向量的条目: %w", err)
	}
	return collectEntries(rows)
}

// setEmbedding 在条目仍是 seen 版本时写入向量；版本变了说明文本已改，这个向量作废。
func (s *Store) setEmbedding(ctx context.Context, id string, seen time.Time, embedding []float32) (bool, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE corpus SET embedding = $3 WHERE id = $1 AND updated_at = $2`,
		id, seen, vectorArg(embedding))
	if err != nil {
		return false, fmt.Errorf("写入向量: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// IndexStats 是向量索引的覆盖情况。
type IndexStats struct {
	Total   int
	Pending int // 尚无向量
}

func (s *Store) indexStats(ctx context.Context) (IndexStats, error) {
	var st IndexStats
	if err := s.pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE embedding IS NULL) FROM corpus`).
		Scan(&st.Total, &st.Pending); err != nil {
		return IndexStats{}, fmt.Errorf("统计知识库索引: %w", err)
	}
	return st, nil
}

// ─── 列表与分面 ──────────────────────────────────────────────────────────────

// ListQuery 是知识库列表的筛选与分页。Scenario 与 General 互斥：前者只看该场景的知识，
// 后者只看通用知识，都不给则不按场景筛选。
type ListQuery struct {
	Search   string
	Scenario string
	General  bool
	Source   string
	Offset   int
	Limit    int
}

// Validate 校验筛选参数。
func (q ListQuery) Validate() error {
	if q.Scenario != "" && q.General {
		return fmt.Errorf("%w：场景与通用筛选不能同时指定", ErrInvalid)
	}
	if q.Source != "" && q.Source != SourceAgent && q.Source != SourceExpert {
		return fmt.Errorf("%w：来源 %q 不合法", ErrInvalid, q.Source)
	}
	return nil
}

// filter 拼出筛选条件；skip 指定的维度不参与（分面计数时每个维度不受自身筛选影响）。
func (q ListQuery) filter(skip string) (string, []any) {
	var conds []string
	var args []any
	add := func(cond string, arg any) {
		args = append(args, arg)
		conds = append(conds, strings.ReplaceAll(cond, "?", "$"+strconv.Itoa(len(args))))
	}
	if q.Search != "" {
		add("(c.title ILIKE ? OR c.content ILIKE ?)", db.ContainsPattern(q.Search)) // 两处共用一个参数位
	}
	if skip != "scenario" {
		switch {
		case q.General:
			conds = append(conds, "c.scenario IS NULL")
		case q.Scenario != "":
			add("c.scenario = ?", q.Scenario)
		}
	}
	if skip != "source" && q.Source != "" {
		add("c.source = ?", q.Source)
	}
	if len(conds) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

func (s *Store) list(ctx context.Context, q ListQuery) ([]Entry, int, error) {
	where, args := q.filter("")
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM corpus c`+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("统计知识条目: %w", err)
	}
	args = append(args, q.Limit, q.Offset)
	rows, err := s.pool.Query(ctx, `SELECT `+entryCols+` FROM corpus c`+where+
		` ORDER BY c.updated_at DESC, c.id LIMIT $`+strconv.Itoa(len(args)-1)+` OFFSET $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return nil, 0, fmt.Errorf("列出知识条目: %w", err)
	}
	items, err := collectEntries(rows)
	if err != nil {
		return nil, 0, err
	}
	if items == nil {
		items = []Entry{}
	}
	return items, total, nil
}

// ScenarioFacet 是按场景的计数（Scenario 为 nil 即通用）。
type ScenarioFacet struct {
	Scenario *string
	Count    int
}

// SourceFacet 是按来源的计数。
type SourceFacet struct {
	Source string
	Count  int
}

// Facets 是列表筛选项的分面计数：每个维度只受其它维度的筛选影响。
type Facets struct {
	Scenarios []ScenarioFacet
	Sources   []SourceFacet
}

func (s *Store) facets(ctx context.Context, q ListQuery) (Facets, error) {
	out := Facets{Scenarios: []ScenarioFacet{}, Sources: []SourceFacet{}}
	where, args := q.filter("scenario")
	rows, err := s.pool.Query(ctx, `SELECT c.scenario, count(*) FROM corpus c`+where+
		` GROUP BY c.scenario ORDER BY c.scenario NULLS FIRST`, args...)
	if err != nil {
		return Facets{}, fmt.Errorf("统计场景分面: %w", err)
	}
	for rows.Next() {
		var f ScenarioFacet
		if err := rows.Scan(&f.Scenario, &f.Count); err != nil {
			rows.Close()
			return Facets{}, fmt.Errorf("读取场景分面: %w", err)
		}
		out.Scenarios = append(out.Scenarios, f)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return Facets{}, fmt.Errorf("遍历场景分面: %w", err)
	}

	where, args = q.filter("source")
	rows, err = s.pool.Query(ctx, `SELECT c.source, count(*) FROM corpus c`+where+
		` GROUP BY c.source ORDER BY c.source`, args...)
	if err != nil {
		return Facets{}, fmt.Errorf("统计来源分面: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var f SourceFacet
		if err := rows.Scan(&f.Source, &f.Count); err != nil {
			return Facets{}, fmt.Errorf("读取来源分面: %w", err)
		}
		out.Sources = append(out.Sources, f)
	}
	return out, rows.Err()
}

// merge 在一个事务里把 absorbed 并入 keep：keep 改写为新文本，absorbed 的印证转给 keep 后删除
// absorbed，再记下 taskID 的印证。任一条目已不是读到时的版本（被改过或删除）返回 ErrStale。
func (s *Store) merge(ctx context.Context, keep Entry, absorbed []Entry, f fields, embedding []float32, taskID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("合并知识条目: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	all := append([]Entry{keep}, absorbed...)
	ids := make([]string, len(all))
	for i, e := range all {
		ids[i] = e.ID
	}
	rows, err := tx.Query(ctx, `SELECT id::text, updated_at FROM corpus WHERE id::text = ANY($1) FOR UPDATE`, ids)
	if err != nil {
		return fmt.Errorf("合并知识条目: %w", err)
	}
	current := map[string]time.Time{}
	for rows.Next() {
		var id string
		var at time.Time
		if err := rows.Scan(&id, &at); err != nil {
			rows.Close()
			return fmt.Errorf("合并知识条目: %w", err)
		}
		current[id] = at
	}
	rows.Close()
	for _, e := range all {
		if at, ok := current[e.ID]; !ok || !at.Equal(e.UpdatedAt) {
			return ErrStale
		}
	}

	absorbedIDs := ids[1:]
	if _, err := tx.Exec(ctx, `
		INSERT INTO corpus_confirmation (entry_id, task_id, confirmed_at)
		SELECT $1, task_id, min(confirmed_at) FROM corpus_confirmation
		WHERE entry_id::text = ANY($2) GROUP BY task_id
		ON CONFLICT DO NOTHING`, keep.ID, absorbedIDs); err != nil {
		return fmt.Errorf("转移印证: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM corpus WHERE id::text = ANY($1)`, absorbedIDs); err != nil {
		return fmt.Errorf("删除被合并的条目: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE corpus SET title = $2, content = $3, content_hash = $4, embedding = $5::vector, updated_at = now()
		WHERE id = $1`, keep.ID, f.Title, f.Content, contentHash(f.Content), vectorArg(embedding)); err != nil {
		if isDuplicate(err) {
			return ErrDuplicate
		}
		return fmt.Errorf("改写合并后的条目: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO corpus_confirmation (entry_id, task_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, keep.ID, taskID); err != nil {
		return fmt.Errorf("记录印证: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("合并知识条目: %w", err)
	}
	return nil
}

// existingHashes 返回 hashes 中已有条目的正文哈希。
func (s *Store) existingHashes(ctx context.Context, hashes []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(hashes) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT content_hash FROM corpus WHERE content_hash = ANY($1)`, hashes)
	if err != nil {
		return nil, fmt.Errorf("查询已有正文: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, fmt.Errorf("查询已有正文: %w", err)
		}
		out[h] = true
	}
	return out, rows.Err()
}
