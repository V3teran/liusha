// Package llmstore 是 LLM 配置（provider 部署 / 复杂度路由）的多级缓存读写层，
// 构建在资源无关的 cachestore 内核之上：内存 L1（本进程）→ redis L2（跨进程共享 + 失效总线）
// → DB（事实源，由 internal/config/llm.Store 打）。
//
// 为何分层：api 与 runner 是**多进程**。前端在 api 改模型配置后，runner 的
// provider.Router 必须在下次 For(complexity) 时读到最新 provider/路由，
// 否则仍按旧部署装配。故写路径写 DB 后经 cachestore 广播失效键，各进程共享的 Subscribe
// goroutine 收到即清本地 L1 + L2，下次读回填最新值。
//
// 路由模型：agent-role → complexity → provider 两跳（complexity 中间层，见 llmcfg.AgentComplexity）。
// 两跳解析全在 llmcfg.Routing.ProviderKeyForRole 内完成，本层只管缓存路由快照 + provider 行。
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
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/cachestore"
	"github.com/V3teran/liusha/internal/config/llm"
)

// llmStore 是 llmstore 依赖的底层持久化能力（*llmcfg.Store 自动满足）。
type llmStore interface {
	CreateProvider(ctx context.Context, p llmcfg.ProviderParams) (llmcfg.Provider, error)
	UpdateProvider(ctx context.Context, p llmcfg.ProviderParams) (llmcfg.Provider, error)
	DeleteProvider(ctx context.Context, key string) error
	GetProvider(ctx context.Context, key string) (llmcfg.Provider, error)
	ListProviders(ctx context.Context, onlyEnabled bool) ([]llmcfg.Provider, error)

	UpsertRoleRoute(ctx context.Context, role, providerKey string) (llmcfg.RoleRoute, error)
	DeleteRoleRoute(ctx context.Context, role string) error
	ListRoleRoutes(ctx context.Context) ([]llmcfg.RoleRoute, error)

	GetRouting(ctx context.Context) (llmcfg.Routing, error)
}

// ComplexityOverrideFunc 按 role 查 DB 里 agent 自定义的复杂度档位（agent.complexity）。
// 返回 (complexity, true) 表示该 role 是有 DB 配置行的 agent 且显式设置了复杂度，覆盖代码内置映射；
// 返回 ("", false) 表示无覆盖（非 agent 的路由 key，或查询失败降级），
// 此时解析回落 llmcfg.AgentComplexity 代码兜底表。查询失败应吞错返 false，不阻塞热路径。
type ComplexityOverrideFunc func(ctx context.Context, role string) (complexity string, ok bool)

// Store 编排 LLM 配置的多级读写：底层 DB store + 共享 cachestore 内核。
type Store struct {
	db                 llmStore
	cache              *cachestore.Cache
	complexityOverride ComplexityOverrideFunc // 可选：DB agent → 复杂度覆盖（nil = 纯走代码映射）
}

// New 用 pgxpool + 共享 cachestore 构造 Store（生产装配用）。
// cache 由进程唯一构造并已 go cache.Subscribe(ctx)，与 configstore 等复用同一实例。
func New(pool *pgxpool.Pool, cache *cachestore.Cache) *Store {
	return newWithStore(llmcfg.NewStore(pool), cache)
}

// WithComplexityOverride 注入 agent → 复杂度覆盖回调（agent.complexity 现读），返回自身便于链式装配。
// api/runner 装配时传入查 agent.complexity 的闭包；不注入则退化为纯代码映射路由。
func (s *Store) WithComplexityOverride(fn ComplexityOverrideFunc) *Store {
	s.complexityOverride = fn
	return s
}

// complexityByCoder 是 AgentComplexityOverride 依赖的最小 agent store 能力（*cfgagent.Store 满足）。
type complexityByCoder interface {
	ComplexityByCode(ctx context.Context, code string) (complexity string, found bool, err error)
}

// AgentComplexityOverride 把 agent store 适配成 ComplexityOverrideFunc（api/runner 共用，避免各写一份）。
// 查询失败吞错返 (,,false)——热路径不因复杂度覆盖读失败而炸，降级到代码映射兜底。
func AgentComplexityOverride(store complexityByCoder, log zerolog.Logger) ComplexityOverrideFunc {
	return func(ctx context.Context, role string) (string, bool) {
		complexity, found, err := store.ComplexityByCode(ctx, role)
		if err != nil {
			log.Warn().Err(err).Str("role", role).Msg("查 agent 复杂度失败（降级：走代码映射兜底）")
			return "", false
		}
		return complexity, found
	}
}

// newWithStore 用已构造的底层 store 装配（测试注入 mock 用）。
func newWithStore(db llmStore, cache *cachestore.Cache) *Store {
	return &Store{db: db, cache: cache}
}

// ── 缓存键（L1/L2 同键，统一前缀 llmstore:）─────────────────────────────

// keyRouting 是路由快照的哨兵键：全部角色路由折进一份 Routing。
// 任一角色路由写即失效它（见 UpsertRoleRoute/DeleteRoleRoute）。
const keyRouting = "llmstore:routing"

func keyProvider(key string) string { return "llmstore:provider:key:" + key }

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

// isNotFound 判定底层 store 的「不存在」——GetProvider/UpdateProvider 用 %w 包 pgx.ErrNoRows，
// upsert 路径据此在 Update 落空时回退 Create。
func isNotFound(err error) bool { return err != nil && errors.Is(err, pgx.ErrNoRows) }

// ── 单条 provider 读（L1/L2 缓存，热路径）───────────────────────────────

// ProviderByKey 按 key 读 provider 部署（For(role) 解析出 key 后取连接参数 + 能力标志）。
func (s *Store) ProviderByKey(ctx context.Context, key string) (llmcfg.Provider, error) {
	return cachestore.ReadThrough(ctx, s.cache, keyProvider(key),
		func(p llmcfg.Provider) []string { return []string{keyProvider(p.Key)} },
		func(ctx context.Context) (llmcfg.Provider, error) {
			return s.db.GetProvider(ctx, key)
		})
}

// Routing 读路由全景快照（role → provider key 的解析全靠它）。缓存于哨兵键。
func (s *Store) Routing(ctx context.Context) (llmcfg.Routing, error) {
	return cachestore.ReadThrough(ctx, s.cache, keyRouting,
		func(llmcfg.Routing) []string { return []string{keyRouting} },
		func(ctx context.Context) (llmcfg.Routing, error) {
			return s.db.GetRouting(ctx)
		})
}

// ── 运行期解析（热路径：两个 LLM 工厂共用，取代旧的双份 resolveProviderKey/lookupLLMField switch）──

// ProviderForRole 把 agent-role 两跳解析到 provider 部署：role → tier → provider key → 部署行。
// 路由快照 + provider 行均走多级缓存。tier 未配置则回退 heavy 档（见 Routing.ProviderKeyForRole）。
// 解析不出 provider key（heavy 档亦缺失）时返回明确错误，绝不静默兜底到任意 provider。
func (s *Store) ProviderForRole(ctx context.Context, role string) (llmcfg.Provider, error) {
	routing, err := s.Routing(ctx)
	if err != nil {
		return llmcfg.Provider{}, err
	}
	// 第一跳 role → complexity：优先 DB agent 自定义档（agent.complexity），否则落代码映射兜底。
	var key string
	if s.complexityOverride != nil {
		if complexity, ok := s.complexityOverride(ctx, role); ok {
			key = routing.ProviderKeyForComplexity(complexity)
		}
	}
	if key == "" {
		key = routing.ProviderKeyForRole(role)
	}
	if key == "" {
		return llmcfg.Provider{}, &UnresolvedError{Role: role}
	}
	return s.ProviderByKey(ctx, key)
}

// ProviderForFallback 解析全局备胎 provider 部署（Router 取 __fallback__ 装配兜底用）。
// 备胎未配置（保留 role __fallback__ 无绑定）时返回明确错误。
func (s *Store) ProviderForFallback(ctx context.Context) (llmcfg.Provider, error) {
	routing, err := s.Routing(ctx)
	if err != nil {
		return llmcfg.Provider{}, err
	}
	key := routing.FallbackProviderKey()
	if key == "" {
		return llmcfg.Provider{}, &UnresolvedError{Fallback: true}
	}
	return s.ProviderByKey(ctx, key)
}

// ── 全量列表读（L1/L2 缓存，key 空间固定）───────────────────────────────

// ListProviders 全量 provider 列表读，走多级缓存（按 onlyEnabled 分键）。
func (s *Store) ListProviders(ctx context.Context, onlyEnabled bool) ([]llmcfg.Provider, error) {
	return cachestore.ReadThrough(ctx, s.cache, keyProvidersList(onlyEnabled),
		func([]llmcfg.Provider) []string { return []string{keyProvidersList(onlyEnabled)} },
		func(ctx context.Context) ([]llmcfg.Provider, error) {
			return s.db.ListProviders(ctx, onlyEnabled)
		})
}

// ListRoleRoutes 全量角色路由列表读，走多级缓存（单键）。
func (s *Store) ListRoleRoutes(ctx context.Context) ([]llmcfg.RoleRoute, error) {
	return cachestore.ReadThrough(ctx, s.cache, keyRoleRoutesList,
		func([]llmcfg.RoleRoute) []string { return []string{keyRoleRoutesList} },
		func(ctx context.Context) ([]llmcfg.RoleRoute, error) {
			return s.db.ListRoleRoutes(ctx)
		})
}

// ── 写（前端 CRUD 走这里，保证跨进程一致）─────────────────────────────
//
// 每个写方法：写 DB → cachestore.Invalidate（本进程即时清 L1+L2 + 广播失效键给其它进程）。

// SaveProvider upsert 一个 provider（按 key：存在则更新、不存在则新建），失效其 provider 键 + 列表键。
// provider 的能力（vision/context_window）参与 role 解析后的取值，但不入 routing 快照，故不失效 routing。
func (s *Store) SaveProvider(ctx context.Context, p llmcfg.ProviderParams) (llmcfg.Provider, error) {
	pr, err := s.db.UpdateProvider(ctx, p)
	if isNotFound(err) {
		pr, err = s.db.CreateProvider(ctx, p)
	}
	if err != nil {
		return llmcfg.Provider{}, err
	}
	if err := s.cache.Invalidate(ctx, providerKeys(pr.Key)...); err != nil {
		return pr, err
	}
	return pr, nil
}

// DeleteProvider 按 key 删 provider，失效其 provider 键 + 列表键。
// 被角色路由 FK 引用（ON DELETE RESTRICT）时撞约束，错误透传给 handler 转 409。
func (s *Store) DeleteProvider(ctx context.Context, key string) error {
	if err := s.db.DeleteProvider(ctx, key); err != nil {
		return err
	}
	return s.cache.Invalidate(ctx, providerKeys(key)...)
}

// UpsertRoleRoute upsert 一个角色 → provider key 直连映射，失效路由快照 + 路由列表键。
func (s *Store) UpsertRoleRoute(ctx context.Context, role, providerKey string) (llmcfg.RoleRoute, error) {
	rr, err := s.db.UpsertRoleRoute(ctx, role, providerKey)
	if err != nil {
		return llmcfg.RoleRoute{}, err
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

// RouterStoreAdapter 把 *Store 包装成 provider.RouterStore（方法名对齐接口）。
type RouterStoreAdapter struct{ s *Store }

// AsRouterStore 返回满足 provider.RouterStore 的适配器。
func (s *Store) AsRouterStore() *RouterStoreAdapter { return &RouterStoreAdapter{s} }

func (a *RouterStoreAdapter) GetRouting(ctx context.Context) (llmcfg.Routing, error) {
	return a.s.Routing(ctx)
}

func (a *RouterStoreAdapter) GetProvider(ctx context.Context, key string) (llmcfg.Provider, error) {
	return a.s.ProviderByKey(ctx, key)
}
