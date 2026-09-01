package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store 封装 agent 配置表的持久化操作。
// Agent表只包含2个固定的内置Agent（Planner和Executor）。
type Store struct {
	pool *pgxpool.Pool
}

// NewStore 用 pgxpool 构造 Store。
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// defaultMaxIterations 是 MaxIterations 留空时的回退值（与 D1 DDL DEFAULT 40 对齐）。
const defaultMaxIterations = 40

// defaultComplexity 是 Complexity 留空时的回退值（与 DDL DEFAULT 'medium' 对齐）。
const defaultComplexity = "medium"

// colsSelect 是所有 SELECT / RETURNING 路径的统一列序，与 scan() 字段一一对应。
const colsSelect = "id, code, kind, name, description, body, function_tools, cli_tools, max_iterations, complexity, enabled, created_at, updated_at"

// validateKind 应用层校验 kind。
func validateKind(k Kind) error {
	switch k {
	case KindPlanner, KindExecutor:
		return nil
	default:
		return fmt.Errorf("非法 kind %q（应为 planner|executor）", k)
	}
}

// validateComplexity 应用层校验 complexity（与 DB CHECK 双保险）；空串合法（Create/Update 落 defaultComplexity）。
func validateComplexity(c string) error {
	switch c {
	case "", "simple", "medium", "complex":
		return nil
	default:
		return fmt.Errorf("非法 complexity %q（应为 simple|medium|complex）", c)
	}
}

// Update 更新Agent配置（只允许更新SystemPrompt、Skills和工具）。
// 不允许修改code、kind、name、description（这些是固定的）。
func (s *Store) Update(ctx context.Context, code string, p UpdateParams) (Agent, error) {
	setParts := []string{}
	args := []any{code}
	argIdx := 2

	if p.SystemPrompt != nil {
		setParts = append(setParts, fmt.Sprintf("body=$%d", argIdx))
		args = append(args, *p.SystemPrompt)
		argIdx++
	}
	if p.FunctionTools != nil {
		toolsJSON, err := marshalTools(*p.FunctionTools)
		if err != nil {
			return Agent{}, fmt.Errorf("marshal function_tools: %w", err)
		}
		setParts = append(setParts, fmt.Sprintf("function_tools=$%d", argIdx))
		args = append(args, toolsJSON)
		argIdx++
	}
	if p.CliTools != nil {
		cliJSON, err := marshalTools(*p.CliTools)
		if err != nil {
			return Agent{}, fmt.Errorf("marshal cli_tools: %w", err)
		}
		setParts = append(setParts, fmt.Sprintf("cli_tools=$%d", argIdx))
		args = append(args, cliJSON)
		argIdx++
	}
	if p.MaxIterations != nil {
		setParts = append(setParts, fmt.Sprintf("max_iterations=$%d", argIdx))
		args = append(args, *p.MaxIterations)
		argIdx++
	}
	if p.Complexity != nil {
		if err := validateComplexity(*p.Complexity); err != nil {
			return Agent{}, err
		}
		setParts = append(setParts, fmt.Sprintf("complexity=$%d", argIdx))
		args = append(args, *p.Complexity)
		argIdx++
	}

	if len(setParts) == 0 {
		return Agent{}, fmt.Errorf("没有要更新的字段")
	}

	setParts = append(setParts, "updated_at=now()")
	query := fmt.Sprintf(`
		UPDATE agent
		SET %s
		WHERE code=$1
		RETURNING `+colsSelect, strings.Join(setParts, ", "))

	row := s.pool.QueryRow(ctx, query, args...)
	var a Agent
	if err := scan(row, &a); err != nil {
		return Agent{}, fmt.Errorf("update agent %q: %w", code, err)
	}
	return a, nil
}

// GetByID 按主键读取。
func (s *Store) GetByID(ctx context.Context, id string) (Agent, error) {
	row := s.pool.QueryRow(ctx, "SELECT "+colsSelect+" FROM agent WHERE id=$1", id)
	var h Agent
	if err := scan(row, &h); err != nil {
		return Agent{}, fmt.Errorf("get executor %s: %w", id, err)
	}
	return h, nil
}

// UpdateComplexity 只改某 agent 的复杂度单字段（分档页「每档选 agent」移档用），不碰其余字段——
// 避免整体 upsert 用列表快照覆盖别处刚改的 body/工具。complexity 必填且须为 simple|medium|complex
// （空串在此拒绝：移档语义要求明确档位，非落 DEFAULT）。返回回读的完整行供上层失效缓存。
func (s *Store) UpdateComplexity(ctx context.Context, id, complexity string) (Agent, error) {
	if complexity == "" {
		return Agent{}, fmt.Errorf("update complexity: complexity 不能为空（应为 simple|medium|complex）")
	}
	if err := validateComplexity(complexity); err != nil {
		return Agent{}, fmt.Errorf("update complexity: %w", err)
	}
	row := s.pool.QueryRow(ctx,
		"UPDATE agent SET complexity=$2, updated_at=now() WHERE id=$1 RETURNING "+colsSelect,
		id, complexity)
	var h Agent
	if err := scan(row, &h); err != nil {
		return Agent{}, fmt.Errorf("update executor complexity %s: %w", id, err)
	}
	return h, nil
}

// ComplexityByCode 只取某 agent 的复杂度档位（LLM 路由第一跳 role→complexity 的 DB 覆盖用，见 llmstore.ComplexityOverrideFunc）。
// found=false 表示无该 code 的 agent 行（非 agent 的路由 key），
// 调用方据此回落代码内置复杂度映射。非「不存在」的真实错误照常返回。
func (s *Store) ComplexityByCode(ctx context.Context, code string) (complexity string, found bool, err error) {
	row := s.pool.QueryRow(ctx, "SELECT complexity FROM agent WHERE code=$1", code)
	switch err := row.Scan(&complexity); {
	case err == nil:
		return complexity, true, nil
	case errors.Is(err, pgx.ErrNoRows):
		return "", false, nil
	default:
		return "", false, fmt.Errorf("get agent complexity %q: %w", code, err)
	}
}

// GetByCode 按稳定引用名读取（代码与种子的主要访问路径）。
func (s *Store) GetByCode(ctx context.Context, code string) (Agent, error) {
	row := s.pool.QueryRow(ctx, "SELECT "+colsSelect+" FROM agent WHERE code=$1", code)
	var h Agent
	if err := scan(row, &h); err != nil {
		return Agent{}, fmt.Errorf("get executor %q: %w", code, err)
	}
	return h, nil
}

// List 按 code 升序列出配置操作员；onlyEnabled=true 时过滤 enabled=false。
func (s *Store) List(ctx context.Context, onlyEnabled bool) ([]Agent, error) {
	q := "SELECT " + colsSelect + " FROM agent"
	if onlyEnabled {
		q += " WHERE enabled=true"
	}
	q += " ORDER BY code ASC"
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list executors: %w", err)
	}
	defer rows.Close()

	var out []Agent
	for rows.Next() {
		var h Agent
		if err := scan(rows, &h); err != nil {
			return nil, fmt.Errorf("scan executor: %w", err)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// ListParams 是分页/搜索列表的入参（配置管理页用；swarm 池仍走 ListEnabledDomain 全量）。
//   - Q     ：按 code/name/description 模糊匹配（空 = 不过滤）
//   - Limit ：<=0 表示不分页（全量）
//   - Offset：分页偏移
type ListParams struct {
	Q      string
	Limit  int
	Offset int
}

// buildFilter 拼装 ListPaged/CountList 共用的 WHERE 子句与参数（DRY）。
func buildFilter(p ListParams) (string, []any) {
	q := strings.TrimSpace(p.Q)
	if q == "" {
		return "", nil
	}
	return " WHERE (code ILIKE $1 OR name ILIKE $1 OR description ILIKE $1)", []any{"%" + q + "%"}
}

// ListPaged 按 code 升序、支持模糊搜索 + 分页列出配置操作员（配置管理页）。
// Limit<=0 时返回过滤后全量。
func (s *Store) ListPaged(ctx context.Context, p ListParams) ([]Agent, error) {
	where, args := buildFilter(p)
	q := "SELECT " + colsSelect + " FROM agent" + where + " ORDER BY code ASC"
	if p.Limit > 0 {
		args = append(args, p.Limit)
		q += fmt.Sprintf(" LIMIT $%d", len(args))
		args = append(args, p.Offset)
		q += fmt.Sprintf(" OFFSET $%d", len(args))
	}
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list executors paged: %w", err)
	}
	defer rows.Close()

	var out []Agent
	for rows.Next() {
		var h Agent
		if err := scan(rows, &h); err != nil {
			return nil, fmt.Errorf("scan executor: %w", err)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// CountList 返回与 ListPaged 相同过滤条件下的总行数（分页 total）。
func (s *Store) CountList(ctx context.Context, p ListParams) (int, error) {
	where, args := buildFilter(p)
	var n int
	if err := s.pool.QueryRow(ctx, "SELECT COUNT(*) FROM agent"+where, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("count executors: %w", err)
	}
	return n, nil
}

// ListEnabledDomain 按 code 升序列出全部 enabled 的领域操作员（kind='domain'）。
// 这是 swarm 引擎的子代理池来源：LLM 运行时在此池内动态 handoff（见 D2）。
func (s *Store) ListEnabledDomain(ctx context.Context) ([]Agent, error) {
	rows, err := s.pool.Query(ctx,
		"SELECT "+colsSelect+" FROM agent WHERE kind='domain' AND enabled=true ORDER BY code ASC")
	if err != nil {
		return nil, fmt.Errorf("list enabled domain executors: %w", err)
	}
	defer rows.Close()

	var out []Agent
	for rows.Next() {
		var h Agent
		if err := scan(rows, &h); err != nil {
			return nil, fmt.Errorf("scan domain executor: %w", err)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// GetPlanner 获取唯一的Planner。
func (s *Store) GetPlanner(ctx context.Context) (Agent, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT `+colsSelect+`
		FROM agent
		WHERE kind='planner' AND enabled=true
		LIMIT 1
	`)
	var a Agent
	if err := scan(row, &a); err != nil {
		return Agent{}, fmt.Errorf("获取planner: %w", err)
	}
	return a, nil
}

// GetExecutor 获取唯一的Executor。
func (s *Store) GetExecutor(ctx context.Context) (Agent, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT `+colsSelect+`
		FROM agent
		WHERE kind='executor' AND enabled=true
		LIMIT 1
	`)
	var a Agent
	if err := scan(row, &a); err != nil {
		return Agent{}, fmt.Errorf("获取executor: %w", err)
	}
	return a, nil
}

// marshalTools 把 []string 序列化为 jsonb；nil 落空数组。
func marshalTools(tools []string) ([]byte, error) {
	if tools == nil {
		tools = []string{}
	}
	b, err := json.Marshal(tools)
	if err != nil {
		return nil, fmt.Errorf("marshal tools: %w", err)
	}
	return b, nil
}

// scanner 抽象 pgx.Row / pgx.Rows 的 Scan 方法。
type scanner interface {
	Scan(dest ...any) error
}

// scan 是 colsSelect 列序的统一反序列化点。
func scan(r scanner, h *Agent) error {
	var kind string
	var fnTools, cliTools []byte
	if err := r.Scan(&h.ID, &h.Code, &kind, &h.Name, &h.Description, &h.SystemPrompt,
		&fnTools, &cliTools, &h.MaxIterations, &h.Complexity, &h.Enabled,
		&h.CreatedAt, &h.UpdatedAt); err != nil {
		return err
	}
	h.Kind = Kind(kind)

	if len(fnTools) > 0 {
		if err := json.Unmarshal(fnTools, &h.FunctionTools); err != nil {
			return fmt.Errorf("unmarshal function_tools: %w", err)
		}
	}
	if len(cliTools) > 0 {
		if err := json.Unmarshal(cliTools, &h.CliTools); err != nil {
			return fmt.Errorf("unmarshal cli_tools: %w", err)
		}
	}
	return nil
}
