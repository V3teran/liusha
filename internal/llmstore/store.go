// Package llmstore 是 LLM 配置（provider 部署 / 复杂度路由）的多级缓存读写层，
// 构建在资源无关的 cachestore 内核之上：内存 L1（本进程）→ redis L2（跨进程共享 + 失效总线）
// → DB（事实源，由 internal/llmconfig.Store 打）。
//
// 为何分层：api 与 runner 是**多进程**。前端在 api 改模型配置后，runner 的
// llm.Router 必须在下次 For(complexity) 时读到最新 provider/路由，
// 否则仍按旧部署装配。故写路径写 DB 后经 cachestore 广播失效键，各进程共享的 Subscribe
// goroutine 收到即清本地 L1 + L2，下次读回填最新值。
//
// 路由模型：档位（simple/medium/complex）→ provider。调用方先定档位（agent 取生效配置的
// complexity，api 侧调用者经 llmconfig.RoleComplexity），档位 → provider 的解析与就近降级在
// framework/llm.Router；本层只管缓存路由快照 + provider 行。
//
// 缓存粒度（key 空间固定的读全走 L1/L2 缓存）：
//   - 单条 provider 读（热路径：每次 For(role) 解析后按 key 取部署）。
//   - 路由快照（全部角色路由，一次读齐），单哨兵键。
//   - 全量列表读：ListProviders（按 onlyEnabled 分 2 键）、ListRoleRoutes（单键）。
//
// 任一 provider/route 写即失效其对应固定键。列表 key 空间有界，L1 无 TTL 也不会堆积。
package llmstore

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/V3teran/liusha/internal/cachestore"
	fwllm "github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/llmconfig"
)

// llmStore 是 llmstore 依赖的底层持久化能力（*llmconfig.Store 自动满足）。
type llmStore interface {
	CreateProvider(ctx context.Context, p llmconfig.ProviderParams) (llmconfig.Provider, error)
	UpdateProvider(ctx context.Context, p llmconfig.ProviderParams) (llmconfig.Provider, error)
	DeleteProvider(ctx context.Context, key string) error
	ClearProviderKey(ctx context.Context, code string) (llmconfig.Provider, error)
	GetProvider(ctx context.Context, key string) (llmconfig.Provider, error)
	ListProviders(ctx context.Context, onlyEnabled bool) ([]llmconfig.Provider, error)

	UpsertRoleRoute(ctx context.Context, role, providerKey string) (llmconfig.RoleRoute, error)
	DeleteRoleRoute(ctx context.Context, role string) error
	ListRoleRoutes(ctx context.Context) ([]llmconfig.RoleRoute, error)

	GetRouting(ctx context.Context) (llmconfig.Routing, error)
}

// Store 编排 LLM 配置的多级读写：底层 DB store + 共享 cachestore 内核。
type Store struct {
	db    llmStore
	cache *cachestore.Cache
}

// New 用 pgxpool + 共享 cachestore 构造 Store（生产装配用）。
// cache 由进程唯一构造并已 go cache.Subscribe(ctx)，与 configstore 等复用同一实例。
func New(pool *pgxpool.Pool, cache *cachestore.Cache) *Store {
	return newWithStore(llmconfig.NewStore(pool), cache)
}

// newWithStore 用已构造的底层 store 装配（测试注入 mock 用）。
func newWithStore(db llmStore, cache *cachestore.Cache) *Store {
	return &Store{db: db, cache: cache}
}

// ── 缓存键（L1/L2 同键，统一前缀 llmstore:）─────────────────────────────

// keyRouting 是路由快照的哨兵键：全部角色路由折进一份 Routing。
// 任一角色路由写即失效它（见 UpsertRoleRoute/DeleteRoleRoute）。
const keyRouting = "llmstore:routing"

func keyProvider(code string) string { return "llmstore:provider:code:" + code }

// keyProvidersList 是全量 provider 列表读的缓存键，按 onlyEnabled 分两键（有界）。
// 任一 provider 写即失效两键（enabled 变动跨 true/false）。
func keyProvidersList(onlyEnabled bool) string {
	if onlyEnabled {
		return "llmstore:providers:list:enabled"
	}
	return "llmstore:providers:list:all"
}

// keyRoleRoutesList 是全量角色路由列表读的缓存键（单键）。任一路由写即失效。
const keyRoleRoutesList = "llmstore:routes:list"

// providerKeys 是一次 provider 写/删要清的全部缓存键：其单条 key 键 + 两个列表键
// （enabled 变动跨 true/false 两表，一律清最省心且正确）。
func providerKeys(key string) []string {
	return []string{keyProvider(key), keyProvidersList(true), keyProvidersList(false)}
}

// routeKeys 是一次角色路由写/删要清的全部缓存键：路由快照哨兵 + 列表键。
func routeKeys() []string {
	return []string{keyRouting, keyRoleRoutesList}
}

// InvalidateAll 清空 LLM 配置的全部读缓存（列表键 + 路由快照 + 路由列表）。
// 供绕过本 store 写路径的批量导入（启动期 seed 直连 DB）之后调用——不经失效
// 广播的外部写入会让缓存里的旧值存活到 TTL，重启后读到「已重建却像未配置」。
func (s *Store) InvalidateAll(ctx context.Context) error {
	return s.cache.Invalidate(ctx, append(routeKeys(), keyProvidersList(true), keyProvidersList(false))...)
}

// ── 单条 provider 读（L1/L2 缓存，热路径）───────────────────────────────

// ProviderByCode 按 code 读 provider 部署（For(role) 解析出 code 后取连接参数 + 能力标志）。
func (s *Store) ProviderByCode(ctx context.Context, key string) (llmconfig.Provider, error) {
	return cachestore.ReadThrough(ctx, s.cache, keyProvider(key),
		func(p llmconfig.Provider) []string { return []string{keyProvider(p.Code)} },
		func(ctx context.Context) (llmconfig.Provider, error) {
			return s.db.GetProvider(ctx, key)
		})
}

// Routing 读路由全景快照（role → provider code 的解析全靠它）。缓存于哨兵键。
func (s *Store) Routing(ctx context.Context) (llmconfig.Routing, error) {
	return cachestore.ReadThrough(ctx, s.cache, keyRouting,
		func(llmconfig.Routing) []string { return []string{keyRouting} },
		func(ctx context.Context) (llmconfig.Routing, error) {
			return s.db.GetRouting(ctx)
		})
}

// ── 运行期解析（热路径：两个 LLM 工厂共用）──

// ── 全量列表读（L1/L2 缓存，key 空间固定）───────────────────────────────

// ListProviders 全量 provider 列表读，走多级缓存（按 onlyEnabled 分键）。
func (s *Store) ListProviders(ctx context.Context, onlyEnabled bool) ([]llmconfig.Provider, error) {
	return cachestore.ReadThrough(ctx, s.cache, keyProvidersList(onlyEnabled),
		func([]llmconfig.Provider) []string { return []string{keyProvidersList(onlyEnabled)} },
		func(ctx context.Context) ([]llmconfig.Provider, error) {
			return s.db.ListProviders(ctx, onlyEnabled)
		})
}

// ListRoleRoutes 全量角色路由列表读，走多级缓存（单键）。
func (s *Store) ListRoleRoutes(ctx context.Context) ([]llmconfig.RoleRoute, error) {
	return cachestore.ReadThrough(ctx, s.cache, keyRoleRoutesList,
		func([]llmconfig.RoleRoute) []string { return []string{keyRoleRoutesList} },
		func(ctx context.Context) ([]llmconfig.RoleRoute, error) {
			return s.db.ListRoleRoutes(ctx)
		})
}

// ── 写（前端 CRUD 走这里，保证跨进程一致）─────────────────────────────
//
// 每个写方法：写 DB → cachestore.Invalidate（本进程即时清 L1+L2 + 广播失效键给其它进程）。

// CreateProvider 新建 provider（code 已存在 → llmconfig.ErrConflict），失效其 provider 键 + 列表键。
func (s *Store) CreateProvider(ctx context.Context, p llmconfig.ProviderParams) (llmconfig.Provider, error) {
	pr, err := s.db.CreateProvider(ctx, p)
	if err != nil {
		return llmconfig.Provider{}, err
	}
	return pr, s.cache.Invalidate(ctx, providerKeys(pr.Code)...)
}

// UpdateProvider 更新既有 provider（不存在 → 包装 pgx.ErrNoRows），失效其 provider 键 + 列表键。
func (s *Store) UpdateProvider(ctx context.Context, p llmconfig.ProviderParams) (llmconfig.Provider, error) {
	pr, err := s.db.UpdateProvider(ctx, p)
	if err != nil {
		return llmconfig.Provider{}, err
	}
	return pr, s.cache.Invalidate(ctx, providerKeys(pr.Code)...)
}

// DeleteProvider 按 key 删 provider，失效其 provider 键 + 列表键。
// 被角色路由 FK 引用（ON DELETE RESTRICT）时撞约束，错误透传给 handler 转 409。
func (s *Store) DeleteProvider(ctx context.Context, key string) error {
	if err := s.db.DeleteProvider(ctx, key); err != nil {
		return err
	}
	return s.cache.Invalidate(ctx, providerKeys(key)...)
}

// ClearProviderKey 清除 provider 的加密密钥（密文+尾号置空），api_key_env 引用原样保留——
// 「恢复使用环境变量」：解析回落 env 路径。失效其 provider 键 + 列表键。
func (s *Store) ClearProviderKey(ctx context.Context, code string) (llmconfig.Provider, error) {
	pr, err := s.db.ClearProviderKey(ctx, code)
	if err != nil {
		return llmconfig.Provider{}, err
	}
	if err := s.cache.Invalidate(ctx, providerKeys(code)...); err != nil {
		return pr, err
	}
	return pr, nil
}

// UpsertRoleRoute upsert 一个角色 → provider code 直连映射，失效路由快照 + 路由列表键。
func (s *Store) UpsertRoleRoute(ctx context.Context, role, providerKey string) (llmconfig.RoleRoute, error) {
	rr, err := s.db.UpsertRoleRoute(ctx, role, providerKey)
	if err != nil {
		return llmconfig.RoleRoute{}, err
	}
	if err := s.cache.Invalidate(ctx, routeKeys()...); err != nil {
		return rr, err
	}
	return rr, nil
}

// DeleteRoleRoute 按 role 删角色路由，失效路由快照 + 路由列表键。
func (s *Store) DeleteRoleRoute(ctx context.Context, role string) error {
	if err := s.db.DeleteRoleRoute(ctx, role); err != nil {
		return err
	}
	return s.cache.Invalidate(ctx, routeKeys()...)
}

// RouterStoreAdapter 把 *Store 包装成 framework llm.RouterStore。
// key 解析（EncryptedAPIKey/ENV 回退）在此完成——框架只收已解析的 ProviderSpec。
type RouterStoreAdapter struct {
	s      *Store
	cipher llmconfig.KeyDecrypter
}

// AsRouterStore 返回满足 llm.RouterStore 的适配器（cipher 用于密钥解密）。
func (s *Store) AsRouterStore(cipher llmconfig.KeyDecrypter) *RouterStoreAdapter {
	return &RouterStoreAdapter{s: s, cipher: cipher}
}

// GetRouting 实现 llm.RouterStore：读 role→档 路由配置。
func (a *RouterStoreAdapter) GetRouting(ctx context.Context) (fwllm.RoutingSpec, error) {
	r, err := a.s.Routing(ctx)
	if err != nil {
		return fwllm.RoutingSpec{}, err
	}
	return fwllm.RoutingSpec{Roles: r.Roles}, nil
}

// GetProviderSpec 实现 llm.RouterStore：按 provider code 读部署行 → 解析密钥 → 框架 spec。
func (a *RouterStoreAdapter) GetProviderSpec(ctx context.Context, key string) (fwllm.ProviderSpec, error) {
	p, err := a.s.ProviderByCode(ctx, key)
	if err != nil {
		return fwllm.ProviderSpec{}, err
	}
	if !p.Enabled {
		// 停用 = 运维显式下线：与密钥缺失同属「未配置」，路由据此降级到相邻档。
		return fwllm.ProviderSpec{}, fmt.Errorf("%w: provider %q 已停用", fwllm.ErrNotConfigured, p.Code)
	}
	apiKey, err := llmconfig.ResolveAPIKey(p, a.cipher)
	if err != nil {
		// 密钥来源缺失（密文与 env 皆空）是「未配置」而非故障：带框架哨兵上抛，
		// 任务据此致命终结并给出配置指引，不进重试。
		return fwllm.ProviderSpec{}, fmt.Errorf("%w: %w", fwllm.ErrNotConfigured, err)
	}
	return fwllm.ProviderSpec{
		Code: p.Code, Type: p.Type, BaseURL: p.BaseURL, Model: p.DefaultModel,
		APIKey: apiKey, AuthScheme: fwllm.AuthScheme(p.AuthScheme),
		OutputLimitParam: fwllm.OutputLimitParam(p.OutputLimitParam),
		MaxTokens:        p.MaxTokens, SupportsVision: p.SupportsVision,
		ThinkingBudget: p.ThinkingBudgetTokens, ContextWindow: p.ContextWindow,
	}, nil
}
