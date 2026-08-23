package llmcfg

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store 封装 llm_provider / llm_role_route 两表的持久化操作（0099 拆别名层后无 llm_alias）。
// 运行期消费不直连本包，走 internal/llmstore（多级缓存 + 跨进程失效）。
type Store struct {
	pool *pgxpool.Pool
}

// NewStore 用 pgxpool 构造 Store。
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// provColsSelect 是 provider 所有 SELECT/RETURNING 的统一列序，与 scanProvider 一一对应。
const provColsSelect = "key, type, base_url, default_model, api_key_env, encrypted_api_key, api_key_last4, max_tokens, supports_tools, supports_vision, context_window, description, sort_order, enabled, created_at, updated_at"

// validateProviderType 应用层校验 type（与 DB CHECK 双保险）。
func validateProviderType(t string) error {
	switch t {
	case ProviderTypeOpenAICompat, ProviderTypeAnthropic:
		return nil
	default:
		return fmt.Errorf("非法 provider type %q（应为 openai_compat|anthropic）", t)
	}
}

// ── provider CRUD ─────────────────────────────────────────────────────

// CreateProvider 插入一行 provider，返回回读完整行。
func (s *Store) CreateProvider(ctx context.Context, p ProviderParams) (Provider, error) {
	if err := validateProviderType(p.Type); err != nil {
		return Provider{}, fmt.Errorf("create provider: %w", err)
	}
	if p.ContextWindow <= 0 {
		return Provider{}, fmt.Errorf("create provider %q: context_window 必须 > 0", p.Key)
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO llm_provider (key, type, base_url, default_model, api_key_env, encrypted_api_key, api_key_last4, max_tokens, supports_tools, supports_vision, context_window, description, sort_order, enabled)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		RETURNING `+provColsSelect,
		p.Key, p.Type, p.BaseURL, p.DefaultModel, nullableText(p.LegacyAPIKeyEnv), p.EncryptedAPIKey, nullableText(p.APIKeyLast4),
		p.MaxTokens, p.SupportsTools, p.SupportsVision, p.ContextWindow, p.Description, p.SortOrder, p.Enabled)
	var pr Provider
	if err := scanProvider(row, &pr); err != nil {
		return Provider{}, fmt.Errorf("create provider %q: %w", p.Key, err)
	}
	return pr, nil
}

// UpdateProvider 按 key 全量更新一行 provider（key 是稳定引用键，不可改）。
// KeepExistingKey=true 时 SET 子句用 COALESCE 跳过 encrypted_api_key 列——前端编辑表单
// 不重新填密钥即保留原密文，不会被本次更新误清空。LegacyAPIKeyEnv 仅 seed 路径会填非空，
// 前端 CRUD 路径始终传空串（置空该列，事实源已转到 encrypted_api_key）。
func (s *Store) UpdateProvider(ctx context.Context, p ProviderParams) (Provider, error) {
	if err := validateProviderType(p.Type); err != nil {
		return Provider{}, fmt.Errorf("update provider: %w", err)
	}
	if p.ContextWindow <= 0 {
		return Provider{}, fmt.Errorf("update provider %q: context_window 必须 > 0", p.Key)
	}
	keyExpr := "$6"       // encrypted_api_key
	last4Expr := "$7"     // api_key_last4（与密钥列成对：改则一起改，保留则一起 COALESCE）
	if p.KeepExistingKey {
		keyExpr = "COALESCE($6, encrypted_api_key)"   // $6=NULL 时保留原密文
		last4Expr = "COALESCE($7, api_key_last4)"      // $7=NULL 时保留原尾号
	}
	row := s.pool.QueryRow(ctx, `
		UPDATE llm_provider
		SET type=$2, base_url=$3, default_model=$4, api_key_env=$5, encrypted_api_key=`+keyExpr+`, api_key_last4=`+last4Expr+`, max_tokens=$8,
		    supports_tools=$9, supports_vision=$10, context_window=$11, description=$12, sort_order=$13, enabled=$14, updated_at=now()
		WHERE key=$1
		RETURNING `+provColsSelect,
		p.Key, p.Type, p.BaseURL, p.DefaultModel, nullableText(p.LegacyAPIKeyEnv), p.EncryptedAPIKey, nullableText(p.APIKeyLast4),
		p.MaxTokens, p.SupportsTools, p.SupportsVision, p.ContextWindow, p.Description, p.SortOrder, p.Enabled)
	var pr Provider
	if err := scanProvider(row, &pr); err != nil {
		return Provider{}, fmt.Errorf("update provider %q: %w", p.Key, err)
	}
	return pr, nil
}

// nullableText 把空串转 nil，避免 seed 之外的写路径把 api_key_env 列误写成空串
// （NULL 才代表「无此旧路径」，与 encrypted_api_key 的 HasStoredKey 判定一致）。
func nullableText(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// DeleteProvider 按 key 删 provider。被角色路由 FK 引用（ON DELETE RESTRICT）时撞约束，错误透传。
func (s *Store) DeleteProvider(ctx context.Context, key string) error {
	tag, err := s.pool.Exec(ctx, "DELETE FROM llm_provider WHERE key=$1", key)
	if err != nil {
		return fmt.Errorf("delete provider %q: %w", key, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("delete provider %q: 不存在", key)
	}
	return nil
}

// GetProvider 按 key 读一行 provider（运行期热路径 + admin :key）。
func (s *Store) GetProvider(ctx context.Context, key string) (Provider, error) {
	row := s.pool.QueryRow(ctx, "SELECT "+provColsSelect+" FROM llm_provider WHERE key=$1", key)
	var pr Provider
	if err := scanProvider(row, &pr); err != nil {
		return Provider{}, fmt.Errorf("get provider %q: %w", key, err)
	}
	return pr, nil
}

// ListProviders 按 sort_order,key 列出全部 provider；onlyEnabled 时过滤 enabled=false。
func (s *Store) ListProviders(ctx context.Context, onlyEnabled bool) ([]Provider, error) {
	q := "SELECT " + provColsSelect + " FROM llm_provider"
	if onlyEnabled {
		q += " WHERE enabled=true"
	}
	q += " ORDER BY sort_order ASC, key ASC"
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

// ── role route（0099 起 role → provider key 直连，无别名层）──────────────

// UpsertRoleRoute 按 role upsert 一个角色 → provider key 直连映射。
func (s *Store) UpsertRoleRoute(ctx context.Context, role, providerKey string) (RoleRoute, error) {
	row := s.pool.QueryRow(ctx, `
		INSERT INTO llm_role_route (role, provider_key)
		VALUES ($1,$2)
		ON CONFLICT (role) DO UPDATE SET provider_key=EXCLUDED.provider_key, updated_at=now()
		RETURNING role, provider_key, created_at, updated_at`,
		role, providerKey)
	var rr RoleRoute
	if err := row.Scan(&rr.Role, &rr.ProviderKey, &rr.CreatedAt, &rr.UpdatedAt); err != nil {
		return RoleRoute{}, fmt.Errorf("upsert role route %q: %w", role, err)
	}
	return rr, nil
}

// DeleteRoleRoute 按 role（能力档 heavy/vision/light）删路由（删后该档回落隐式默认档 heavy）。
func (s *Store) DeleteRoleRoute(ctx context.Context, role string) error {
	tag, err := s.pool.Exec(ctx, "DELETE FROM llm_role_route WHERE role=$1", role)
	if err != nil {
		return fmt.Errorf("delete role route %q: %w", role, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("delete role route %q: 不存在", role)
	}
	return nil
}

// ListRoleRoutes 按 role 列出全部路由（能力档 heavy/vision/light + 保留 role __fallback__）。
func (s *Store) ListRoleRoutes(ctx context.Context) ([]RoleRoute, error) {
	rows, err := s.pool.Query(ctx, "SELECT role, provider_key, created_at, updated_at FROM llm_role_route ORDER BY role ASC")
	if err != nil {
		return nil, fmt.Errorf("list role routes: %w", err)
	}
	defer rows.Close()
	var out []RoleRoute
	for rows.Next() {
		var rr RoleRoute
		if err := rows.Scan(&rr.Role, &rr.ProviderKey, &rr.CreatedAt, &rr.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan role route: %w", err)
		}
		out = append(out, rr)
	}
	return out, rows.Err()
}

// GetRouting 一次性读齐全部角色路由，拼成 Routing 快照（运行期 role → provider key 解析用）。
func (s *Store) GetRouting(ctx context.Context) (Routing, error) {
	roles, err := s.ListRoleRoutes(ctx)
	if err != nil {
		return Routing{}, err
	}
	r := Routing{Roles: make(map[string]string, len(roles))}
	for _, rr := range roles {
		r.Roles[rr.Role] = rr.ProviderKey
	}
	return r, nil
}

// ── scan ──────────────────────────────────────────────────────────────

type scanner interface {
	Scan(dest ...any) error
}

// scanProvider 是 provColsSelect 列序的统一反序列化点。
// api_key_env 列（migration 0103 起可空）经 sql.NullString 中转，NULL 时 Provider.APIKeyEnv 留空串。
func scanProvider(r scanner, p *Provider) error {
	var apiKeyEnv, apiKeyLast4 sql.NullString
	if err := r.Scan(&p.Key, &p.Type, &p.BaseURL, &p.DefaultModel, &apiKeyEnv, &p.EncryptedAPIKey, &apiKeyLast4,
		&p.MaxTokens, &p.SupportsTools, &p.SupportsVision, &p.ContextWindow,
		&p.Description, &p.SortOrder, &p.Enabled, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return err
	}
	p.APIKeyEnv = apiKeyEnv.String
	p.APIKeyLast4 = apiKeyLast4.String
	return nil
}
