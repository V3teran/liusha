package llmconfig

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/V3teran/liusha/internal/db"
)

// Store 封装 llm_provider / llm_role_route 两表的持久化操作。
// 运行期消费不直连本包，走 internal/llmstore（多级缓存 + 跨进程失效）。
type Store struct {
	pool *pgxpool.Pool
}

// NewStore 用 pgxpool 构造 Store。
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// provColsSelect 是 provider 所有 SELECT/RETURNING 的统一列序，与 scanProvider 一一对应。
const provColsSelect = "code, type, base_url, default_model, encrypted_api_key, api_key_last4, auth_scheme, output_limit_param, max_tokens, supports_tools, supports_vision, context_window, display_name, description, sort_order, enabled, reasoning_effort, created_at, updated_at"

// ── provider CRUD ─────────────────────────────────────────────────────

// CreateProvider 插入一行 provider，返回回读完整行。
func (s *Store) CreateProvider(ctx context.Context, p ProviderParams) (Provider, error) {
	p.Normalize()
	if err := p.Validate(); err != nil {
		return Provider{}, err
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO llm_provider (code, type, base_url, default_model, encrypted_api_key, api_key_last4, max_tokens, supports_tools, supports_vision, context_window, display_name, description, sort_order, enabled, reasoning_effort, auth_scheme, output_limit_param)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
		RETURNING `+provColsSelect,
		p.Code, p.Type, p.BaseURL, p.DefaultModel, p.EncryptedAPIKey, nullableText(p.APIKeyLast4),
		p.MaxTokens, p.SupportsTools, p.SupportsVision, p.ContextWindow, p.DisplayName, p.Description, p.SortOrder, p.Enabled, p.ReasoningEffort,
		p.AuthScheme, p.OutputLimitParam)
	var pr Provider
	if err := scanProvider(row, &pr); err != nil {
		if conflict := uniqueViolation(err, p); conflict != nil {
			return Provider{}, conflict
		}
		return Provider{}, fmt.Errorf("create provider %q: %w", p.Code, err)
	}
	return pr, nil
}

// uniqueViolation 把唯一约束冲突翻译为面向操作者的 ErrConflict：显示名称重名，或 code 已存在；
// 其余错误返回 nil。
func uniqueViolation(err error, p ProviderParams) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		return nil
	}
	if pgErr.ConstraintName == displayNameUniqueIndex {
		return conflictf("显示名称 %q 已被其他 provider 使用", p.DisplayName)
	}
	return conflictf("provider %q 已存在", p.Code)
}

// displayNameUniqueIndex 是显示名称不区分大小写唯一的索引名（迁移 0204）。
const displayNameUniqueIndex = "uq_llm_provider_display_name"

// UpdateProvider 按 code 全量更新一行 provider（code 是稳定引用标识，不可改）；
// 行不存在时包装 pgx.ErrNoRows。
// KeepExistingKey=true 时 SET 子句用 COALESCE 跳过 encrypted_api_key 列——前端编辑表单
// 不重新填密钥即保留原密文，不会被本次更新误清空。
func (s *Store) UpdateProvider(ctx context.Context, p ProviderParams) (Provider, error) {
	p.Normalize()
	if err := p.Validate(); err != nil {
		return Provider{}, err
	}
	keyExpr := "$5"   // encrypted_api_key
	last4Expr := "$6" // api_key_last4（与密钥列成对：改则一起改，保留则一起 COALESCE）
	if p.KeepExistingKey {
		keyExpr = "COALESCE($5, encrypted_api_key)" // $5=NULL 时保留原密文
		last4Expr = "COALESCE($6, api_key_last4)"   // $6=NULL 时保留原尾号
	}
	row := s.pool.QueryRow(ctx, `
		UPDATE llm_provider
		SET type=$2, base_url=$3, default_model=$4, encrypted_api_key=`+keyExpr+`, api_key_last4=`+last4Expr+`, max_tokens=$7,
		    supports_tools=$8, supports_vision=$9, context_window=$10, display_name=$11, description=$12, sort_order=$13, enabled=$14, reasoning_effort=$15,
		    auth_scheme=$16, output_limit_param=$17, updated_at=now()
		WHERE code=$1
		RETURNING `+provColsSelect,
		p.Code, p.Type, p.BaseURL, p.DefaultModel, p.EncryptedAPIKey, nullableText(p.APIKeyLast4),
		p.MaxTokens, p.SupportsTools, p.SupportsVision, p.ContextWindow, p.DisplayName, p.Description, p.SortOrder, p.Enabled, p.ReasoningEffort,
		p.AuthScheme, p.OutputLimitParam)
	var pr Provider
	if err := scanProvider(row, &pr); err != nil {
		if conflict := uniqueViolation(err, p); conflict != nil {
			return Provider{}, conflict
		}
		return Provider{}, fmt.Errorf("update provider %q: %w", p.Code, err)
	}
	return pr, nil
}

// nullableText 把空串转 nil：尾号与密文成对，KeepExistingKey 时 NULL 经 COALESCE 保留原值。
func nullableText(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// DeleteProvider 按 code 删 provider。被角色路由 FK 引用（ON DELETE RESTRICT）时撞约束，错误透传。
func (s *Store) DeleteProvider(ctx context.Context, code string) error {
	tag, err := s.pool.Exec(ctx, "DELETE FROM llm_provider WHERE code=$1", code)
	if err != nil {
		return fmt.Errorf("delete provider %q: %w", code, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("delete provider %q: %w", code, pgx.ErrNoRows)
	}
	return nil
}

// GetProvider 按 key 读一行 provider（运行期热路径 + admin :key）。
func (s *Store) GetProvider(ctx context.Context, code string) (Provider, error) {
	row := s.pool.QueryRow(ctx, "SELECT "+provColsSelect+" FROM llm_provider WHERE code=$1", code)
	var pr Provider
	if err := scanProvider(row, &pr); err != nil {
		return Provider{}, fmt.Errorf("get provider %q: %w", code, err)
	}
	return pr, nil
}

// ListProviders 按 sort_order,key 列出全部 provider；onlyEnabled 时过滤 enabled=false。
func (s *Store) ListProviders(ctx context.Context, onlyEnabled bool) ([]Provider, error) {
	q := "SELECT " + provColsSelect + " FROM llm_provider"
	if onlyEnabled {
		q += " WHERE enabled=true"
	}
	q += " ORDER BY sort_order ASC, code ASC"
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list providers: %w", err)
	}
	defer rows.Close()
	var out []Provider
	for rows.Next() {
		var pr Provider
		if err := scanProvider(rows, &pr); err != nil {
			return nil, fmt.Errorf("scan provider: %w", err)
		}
		out = append(out, pr)
	}
	return out, rows.Err()
}

// ── role route（档位 → provider code）──────────────────────────────────

// UpsertRoleRoute 按档 upsert 一条档 → provider code 映射；只接受三档之一
// （删除不设限，供清理历史遗留行）。
func (s *Store) UpsertRoleRoute(ctx context.Context, role, providerCode string) (RoleRoute, error) {
	if !IsComplexity(role) {
		return RoleRoute{}, invalidf("非法档位 %q（应为 simple、medium 或 complex）", role)
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO llm_role_route (role, provider_code)
		VALUES ($1,$2)
		ON CONFLICT (role) DO UPDATE SET provider_code=EXCLUDED.provider_code, updated_at=now()
		RETURNING role, provider_code, created_at, updated_at`,
		role, providerCode)
	var rr RoleRoute
	if err := row.Scan(&rr.Role, &rr.ProviderCode, &rr.CreatedAt, &rr.UpdatedAt); err != nil {
		return RoleRoute{}, fmt.Errorf("upsert role route %q: %w", role, err)
	}
	return rr, nil
}

// DeleteRoleRoute 按 role（能力档 simple/medium/complex）删路由。
// 幂等：路由已不存在（RowsAffected=0）时静默成功——双开页面先后「选空解绑」时，
// 后到的那次不该收到 500 假性失败。
func (s *Store) DeleteRoleRoute(ctx context.Context, role string) error {
	if _, err := s.pool.Exec(ctx, "DELETE FROM llm_role_route WHERE role=$1", role); err != nil {
		return fmt.Errorf("delete role route %q: %w", role, err)
	}
	return nil
}

// ListRoleRoutes 按 role 列出全部路由（能力档 simple/medium/complex；自定义/历史遗留档也会出现）。
func (s *Store) ListRoleRoutes(ctx context.Context) ([]RoleRoute, error) {
	rows, err := s.pool.Query(ctx, "SELECT role, provider_code, created_at, updated_at FROM llm_role_route ORDER BY role ASC")
	if err != nil {
		return nil, fmt.Errorf("list role routes: %w", err)
	}
	defer rows.Close()
	var out []RoleRoute
	for rows.Next() {
		var rr RoleRoute
		if err := rows.Scan(&rr.Role, &rr.ProviderCode, &rr.CreatedAt, &rr.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan role route: %w", err)
		}
		out = append(out, rr)
	}
	return out, rows.Err()
}

// GetRouting 一次性读齐全部角色路由，拼成 Routing 快照（运行期 role → provider code 解析用）。
func (s *Store) GetRouting(ctx context.Context) (Routing, error) {
	roles, err := s.ListRoleRoutes(ctx)
	if err != nil {
		return Routing{}, err
	}
	r := Routing{Roles: make(map[string]string, len(roles))}
	for _, rr := range roles {
		r.Roles[rr.Role] = rr.ProviderCode
	}
	return r, nil
}

// ── scan ──────────────────────────────────────────────────────────────

// scanProvider 是 provColsSelect 列序的统一反序列化点。
// api_key_last4 列可空，经 sql.NullString 中转，NULL 时留空串。
func scanProvider(r db.RowScanner, p *Provider) error {
	var apiKeyLast4 sql.NullString
	var displayName sql.NullString
	if err := r.Scan(&p.Code, &p.Type, &p.BaseURL, &p.DefaultModel, &p.EncryptedAPIKey, &apiKeyLast4,
		&p.AuthScheme, &p.OutputLimitParam, &p.MaxTokens, &p.SupportsTools, &p.SupportsVision, &p.ContextWindow, &displayName,
		&p.Description, &p.SortOrder, &p.Enabled, &p.ReasoningEffort, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return err
	}
	p.APIKeyLast4 = apiKeyLast4.String
	p.DisplayName = displayName.String
	return nil
}
