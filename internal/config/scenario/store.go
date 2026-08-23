package scenario

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store 封装 scenario 表的持久化操作。
type Store struct {
	pool *pgxpool.Pool
}

// NewStore 用 pgxpool 构造 Store。
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// colsSelect 是所有 SELECT / RETURNING 路径的统一列序，与 scan() 字段一一对应。
const colsSelect = "id, code, name, description, instruction, engine, solo_executor_id::text, enabled, created_at, updated_at"

// validateParams 应用层校验 engine 与 solo_executor_id 的耦合（与 DB CHECK 双保险）：
// solo 必须指定 solo_executor_id，swarm 必须为空。
func validateParams(p NewParams) error {
	switch p.Engine {
	case EngineSolo:
		if p.SoloExecutorID == nil || *p.SoloExecutorID == "" {
			return fmt.Errorf("solo 场景必须指定 solo_executor_id")
		}
	case EngineSwarm:
		if p.SoloExecutorID != nil && *p.SoloExecutorID != "" {
			return fmt.Errorf("swarm 场景不得指定 solo_executor_id")
		}
	default:
		return fmt.Errorf("非法 engine %q（应为 solo|swarm）", p.Engine)
	}
	return nil
}

// Create 插入一行场景，返回回读的完整行。
func (s *Store) Create(ctx context.Context, p NewParams) (Scenario, error) {
	if err := validateParams(p); err != nil {
		return Scenario{}, fmt.Errorf("create scenario: %w", err)
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO scenario (code, name, description, instruction, engine, solo_executor_id, enabled)
		VALUES ($1, $2, $3, $4, $5, $6::uuid, $7)
		RETURNING `+colsSelect,
		p.Code, p.Name, p.Description, p.Instruction,
		p.Engine, p.SoloExecutorID, p.Enabled)
	var sc Scenario
	if err := scan(row, &sc); err != nil {
		return Scenario{}, fmt.Errorf("create scenario %q: %w", p.Code, err)
	}
	return sc, nil
}

// Update 按 code 全量更新一行场景。
func (s *Store) Update(ctx context.Context, p NewParams) (Scenario, error) {
	if err := validateParams(p); err != nil {
		return Scenario{}, fmt.Errorf("update scenario: %w", err)
	}
	row := s.pool.QueryRow(ctx, `
		UPDATE scenario
		SET name=$2, description=$3, instruction=$4, engine=$5, solo_executor_id=$6::uuid, enabled=$7, updated_at=now()
		WHERE code=$1
		RETURNING `+colsSelect,
		p.Code, p.Name, p.Description, p.Instruction,
		p.Engine, p.SoloExecutorID, p.Enabled)
	var sc Scenario
	if err := scan(row, &sc); err != nil {
		return Scenario{}, fmt.Errorf("update scenario %q: %w", p.Code, err)
	}
	return sc, nil
}

// Delete 按 code 删除场景。task.scenario_id 是裸 text 无 FK，删场景不影响历史 task（见 D3）。
func (s *Store) Delete(ctx context.Context, code string) error {
	tag, err := s.pool.Exec(ctx, "DELETE FROM scenario WHERE code=$1", code)
	if err != nil {
		return fmt.Errorf("delete scenario %q: %w", code, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("delete scenario %q: 不存在", code)
	}
	return nil
}

// GetByID 按主键读取（admin CRUD :id 用）。
func (s *Store) GetByID(ctx context.Context, id string) (Scenario, error) {
	row := s.pool.QueryRow(ctx, "SELECT "+colsSelect+" FROM scenario WHERE id=$1", id)
	var sc Scenario
	if err := scan(row, &sc); err != nil {
		return Scenario{}, fmt.Errorf("get scenario %s: %w", id, err)
	}
	return sc, nil
}

// GetByCode 按 code 读取（运行期派发热路径——task.scenario_id 存的是 code，见 D3）。
func (s *Store) GetByCode(ctx context.Context, code string) (Scenario, error) {
	row := s.pool.QueryRow(ctx, "SELECT "+colsSelect+" FROM scenario WHERE code=$1", code)
	var sc Scenario
	if err := scan(row, &sc); err != nil {
		return Scenario{}, fmt.Errorf("get scenario %q: %w", code, err)
	}
	return sc, nil
}

// List 按 code 升序列出场景；onlyEnabled=true 时过滤 enabled=false。
func (s *Store) List(ctx context.Context, onlyEnabled bool) ([]Scenario, error) {
	q := "SELECT " + colsSelect + " FROM scenario"
	if onlyEnabled {
		q += " WHERE enabled=true"
	}
	q += " ORDER BY code ASC"
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list scenarios: %w", err)
	}
	defer rows.Close()

	var out []Scenario
	for rows.Next() {
		var sc Scenario
		if err := scan(rows, &sc); err != nil {
			return nil, fmt.Errorf("scan scenario: %w", err)
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

// ListParams 是分页/搜索列表的入参（配置管理页用；picker 仍走全量 List）。
//   - Q     ：按 code/name/description 模糊匹配（空 = 不过滤）
//   - Limit ：<=0 表示不分页（全量）
//   - Offset：分页偏移
type ListParams struct {
	Q      string
	Limit  int
	Offset int
}

// buildFilter 拼装 ListPaged/Count 共用的 WHERE 子句与参数（DRY）。
func buildFilter(p ListParams) (string, []any) {
	q := strings.TrimSpace(p.Q)
	if q == "" {
		return "", nil
	}
	return " WHERE (code ILIKE $1 OR name ILIKE $1 OR description ILIKE $1)", []any{"%" + q + "%"}
}

// ListPaged 按 code 升序、支持模糊搜索 + 分页列出场景（配置管理页）。
// Limit<=0 时返回过滤后全量。
func (s *Store) ListPaged(ctx context.Context, p ListParams) ([]Scenario, error) {
	where, args := buildFilter(p)
	q := "SELECT " + colsSelect + " FROM scenario" + where + " ORDER BY code ASC"
	if p.Limit > 0 {
		args = append(args, p.Limit)
		q += fmt.Sprintf(" LIMIT $%d", len(args))
		args = append(args, p.Offset)
		q += fmt.Sprintf(" OFFSET $%d", len(args))
	}
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list scenarios paged: %w", err)
	}
	defer rows.Close()

	var out []Scenario
	for rows.Next() {
		var sc Scenario
		if err := scan(rows, &sc); err != nil {
			return nil, fmt.Errorf("scan scenario: %w", err)
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

// Count 返回与 ListPaged 相同过滤条件下的总行数（分页 total）。
func (s *Store) Count(ctx context.Context, p ListParams) (int, error) {
	where, args := buildFilter(p)
	var n int
	if err := s.pool.QueryRow(ctx, "SELECT COUNT(*) FROM scenario"+where, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("count scenarios: %w", err)
	}
	return n, nil
}

// scanner 抽象 pgx.Row / pgx.Rows 的 Scan 方法。
type scanner interface {
	Scan(dest ...any) error
}

// scan 是 colsSelect 列序的统一反序列化点。
func scan(r scanner, sc *Scenario) error {
	return r.Scan(&sc.ID, &sc.Code, &sc.Name, &sc.Description, &sc.Instruction,
		&sc.Engine, &sc.SoloExecutorID, &sc.Enabled, &sc.CreatedAt, &sc.UpdatedAt)
}
