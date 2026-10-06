package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store 封装 agent 配置表的持久化操作。
// agent 表只含四个内置角色（planner/executor/evaluator/monitor，以 code 寻址）。
type Store struct {
	pool *pgxpool.Pool
}

// NewStore 用 pgxpool 构造 Store。
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// colsSelect 是所有 SELECT / RETURNING 路径的统一列序，与 scan() 字段一一对应。
const colsSelect = "id, code, name, description, system_prompt, function_tools, cli_tools, skills, max_iterations, complexity, enabled, created_at, updated_at"

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
// 不允许修改code、name、description（这些是固定的）。
func (s *Store) Update(ctx context.Context, code string, p UpdateParams) (Agent, error) {
	setParts := []string{}
	args := []any{code}
	argIdx := 2

	if p.SystemPrompt != nil {
		setParts = append(setParts, fmt.Sprintf("system_prompt=$%d", argIdx))
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
	if p.Skills != nil {
		skillsJSON, err := marshalTools(*p.Skills)
		if err != nil {
			return Agent{}, fmt.Errorf("marshal skills: %w", err)
		}
		setParts = append(setParts, fmt.Sprintf("skills=$%d", argIdx))
		args = append(args, skillsJSON)
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
		// argIdx++ 是最后一个参数，无需递增
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
		return Agent{}, fmt.Errorf("get agent %s: %w", id, err)
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
		return Agent{}, fmt.Errorf("update agent complexity %s: %w", id, err)
	}
	return h, nil
}

// GetByCode 按稳定引用名读取（代码与种子的主要访问路径——四角色即四个固定 code）。
func (s *Store) GetByCode(ctx context.Context, code string) (Agent, error) {
	row := s.pool.QueryRow(ctx, "SELECT "+colsSelect+" FROM agent WHERE code=$1", code)
	var h Agent
	if err := scan(row, &h); err != nil {
		return Agent{}, fmt.Errorf("get agent %q: %w", code, err)
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
		return nil, fmt.Errorf("list agents: %w", err)
	}
	defer rows.Close()

	var out []Agent
	for rows.Next() {
		var h Agent
		if err := scan(rows, &h); err != nil {
			return nil, fmt.Errorf("scan agent: %w", err)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// ListParams 是分页/搜索列表的入参（配置管理页用；运行时装配按固定 code 直读）。
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
		return nil, fmt.Errorf("list agents paged: %w", err)
	}
	defer rows.Close()

	var out []Agent
	for rows.Next() {
		var h Agent
		if err := scan(rows, &h); err != nil {
			return nil, fmt.Errorf("scan agent: %w", err)
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
		return 0, fmt.Errorf("count agents: %w", err)
	}
	return n, nil
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
	var fnTools, cliTools, skills []byte
	if err := r.Scan(&h.ID, &h.Code, &h.Name, &h.Description, &h.SystemPrompt,
		&fnTools, &cliTools, &skills, &h.MaxIterations, &h.Complexity, &h.Enabled,
		&h.CreatedAt, &h.UpdatedAt); err != nil {
		return err
	}

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
	if len(skills) > 0 {
		if err := json.Unmarshal(skills, &h.Skills); err != nil {
			return fmt.Errorf("unmarshal skills: %w", err)
		}
	}
	return nil
}

// Upsert 按 code 插入或覆盖（种子导入 / reset 语义专用；常规 CRUD 走 Update）。
//
// ON CONFLICT 分支不触碰 enabled：enabled 是运维开关，种子不应反转既有值。
func (s *Store) Upsert(ctx context.Context, code, name, description, systemPrompt string, p UpdateParams) (Agent, error) {
	if code == "" {
		return Agent{}, fmt.Errorf("upsert agent: code 必填")
	}
	if systemPrompt == "" {
		return Agent{}, fmt.Errorf("upsert agent %q: system_prompt 必填", code)
	}
	complexity := "medium"
	if p.Complexity != nil && *p.Complexity != "" {
		if err := validateComplexity(*p.Complexity); err != nil {
			return Agent{}, err
		}
		complexity = *p.Complexity
	}

	var fnTools, cliTools, skills []byte
	var err error
	if p.FunctionTools != nil {
		if fnTools, err = marshalTools(*p.FunctionTools); err != nil {
			return Agent{}, fmt.Errorf("marshal function_tools: %w", err)
		}
	}
	if p.CliTools != nil {
		if cliTools, err = marshalTools(*p.CliTools); err != nil {
			return Agent{}, fmt.Errorf("marshal cli_tools: %w", err)
		}
	}
	if p.Skills != nil {
		if skills, err = marshalTools(*p.Skills); err != nil {
			return Agent{}, fmt.Errorf("marshal skills: %w", err)
		}
	}
	maxIter := 30
	if p.MaxIterations != nil {
		maxIter = *p.MaxIterations
	}

	query := `
		INSERT INTO agent (code, name, description, system_prompt,
			function_tools, cli_tools, skills, max_iterations, complexity, enabled)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, true)
		ON CONFLICT (code) DO UPDATE SET
			name = EXCLUDED.name,
			description = EXCLUDED.description,
			system_prompt = EXCLUDED.system_prompt,
			function_tools = EXCLUDED.function_tools,
			cli_tools = EXCLUDED.cli_tools,
			skills = EXCLUDED.skills,
			max_iterations = EXCLUDED.max_iterations,
			complexity = EXCLUDED.complexity,
			updated_at = now()
		RETURNING ` + colsSelect

	row := s.pool.QueryRow(ctx, query, code, name, description, systemPrompt,
		fnTools, cliTools, skills, maxIter, complexity)
	var a Agent
	if err := scan(row, &a); err != nil {
		return Agent{}, fmt.Errorf("upsert agent %q: %w", code, err)
	}
	return a, nil
}
