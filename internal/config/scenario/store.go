package scenario

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store 封装 scenario 表的持久化操作。
type Store struct {
	pool *pgxpool.Pool
}

// NewStore 用 pgxpool 构造 Store。
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// defaultDomain 是 Domain 留空时的回退值（与 D1 DDL DEFAULT 'web' 对齐）。
const defaultDomain = "web"

// colsSelect 是所有 SELECT / RETURNING 路径的统一列序，与 scan() 字段一一对应。
const colsSelect = "id, code, name, description, instruction, domain, engine, playbook_id::text, enabled, created_at, updated_at"

// validateEngine 应用层校验 engine（与 DB CHECK 双保险）。
func validateEngine(e string) error {
	switch e {
	case EngineSolo, EngineSwarm:
		return nil
	default:
		return fmt.Errorf("非法 engine %q（应为 solo|swarm）", e)
	}
}

// normalizeDomain 把空 domain 折成默认值 web（应用层兜底，DB 也有 DEFAULT）。
func normalizeDomain(d string) string {
	if d == "" {
		return defaultDomain
	}
	return d
}

// Create 插入一行场景，返回回读的完整行。
func (s *Store) Create(ctx context.Context, p NewParams) (Scenario, error) {
	if err := validateEngine(p.Engine); err != nil {
		return Scenario{}, fmt.Errorf("create scenario: %w", err)
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO scenario (code, name, description, instruction, domain, engine, playbook_id, enabled)
		VALUES ($1, $2, $3, $4, $5, $6, $7::uuid, $8)
		RETURNING `+colsSelect,
		p.Code, p.Name, p.Description, p.Instruction, normalizeDomain(p.Domain),
		p.Engine, p.PlaybookID, p.Enabled)
	var sc Scenario
	if err := scan(row, &sc); err != nil {
		return Scenario{}, fmt.Errorf("create scenario %q: %w", p.Code, err)
	}
	return sc, nil
}

// Update 按 code 全量更新一行场景。
func (s *Store) Update(ctx context.Context, p NewParams) (Scenario, error) {
	if err := validateEngine(p.Engine); err != nil {
		return Scenario{}, fmt.Errorf("update scenario: %w", err)
	}
	row := s.pool.QueryRow(ctx, `
		UPDATE scenario
		SET name=$2, description=$3, instruction=$4, domain=$5, engine=$6, playbook_id=$7::uuid, enabled=$8, updated_at=now()
		WHERE code=$1
		RETURNING `+colsSelect,
		p.Code, p.Name, p.Description, p.Instruction, normalizeDomain(p.Domain),
		p.Engine, p.PlaybookID, p.Enabled)
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

// scanner 抽象 pgx.Row / pgx.Rows 的 Scan 方法。
type scanner interface {
	Scan(dest ...any) error
}

// scan 是 colsSelect 列序的统一反序列化点。
func scan(r scanner, sc *Scenario) error {
	return r.Scan(&sc.ID, &sc.Code, &sc.Name, &sc.Description, &sc.Instruction,
		&sc.Domain, &sc.Engine, &sc.PlaybookID, &sc.Enabled, &sc.CreatedAt, &sc.UpdatedAt)
}
