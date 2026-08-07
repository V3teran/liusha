// Package llmstore 是 LLM 配置（provider 部署 / 角色路由）的多级缓存读写层，
// 构建在资源无关的 cachestore 内核之上：内存 L1（本进程）→ redis L2（跨进程共享 + 失效总线）
// → DB（事实源，由 internal/config/llmcfg.Store 打）。
//
// 为何分层（见 D7）：api 与 runner 是**多进程**。前端在 api 改模型配置后，runner 的两个
// LLM 工厂（internal/llm / internal/einollm）必须在下次 For(role) 时读到最新 provider/路由，
// 否则仍按旧部署装配。故写路径写 DB 后经 cachestore 广播失效键，各进程共享的 Subscribe
// goroutine 收到即清本地 L1 + L2，下次读回填最新值。
//
// 路由模型（0099 起）：role → provider **一跳直连**（无别名中间层）。
//
// 缓存粒度：
//   - 单条 provider 读（热路径：每次 For(role) 解析后按 key 取部署）走 L1/L2 缓存。
//   - 路由快照（全部角色路由，一次读齐）走 L1/L2 缓存，单哨兵键。
//   - 列表读（admin CRUD 列表页）低频且失效成本高，直穿 DB 不缓存。
package llmstore

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/V3teran/liusha/internal/cachestore"
	"github.com/V3teran/liusha/internal/config/llmcfg"
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

// Store 编排 LLM 配置的多级读写：底层 DB store + 共享 cachestore 内核。
type Store struct {
	db    llmStore
	cache *cachestore.Cache
}

// New 用 pgxpool + 共享 cachestore 构造 Store（生产装配用）。
// cache 由进程唯一构造并已 go cache.Subscribe(ctx)，与 configstore 等复用同一实例。
func New(pool *pgxpool.Pool, cache *cachestore.Cache) *Store {
	return newWithStore(llmcfg.NewStore(pool), cache)
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

// ProviderForRole 把 role 一站式解析到 provider 部署：role → provider key → 部署行（一跳直连）。
// 路由快照 + provider 行均走多级缓存。role 未命中角色路由则回退 __default__ 兜底（见 Routing.ProviderKeyForRole）。
// 解析不出 provider key（__default__ 兜底缺失）时返回明确错误，绝不静默兜底到任意 provider。
func (s *Store) ProviderForRole(ctx context.Context, role string) (llmcfg.Provider, error) {
	routing, err := s.Routing(ctx)
	if err != nil {
		return llmcfg.Provider{}, err
	}
	key := routing.ProviderKeyForRole(role)
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

// ── 列表读（不缓存，直穿 DB，admin CRUD）───────────────────────────────

// ListProviders 直穿底层 store（admin 列表页低频）。
func (s *Store) ListProviders(ctx context.Context, onlyEnabled bool) ([]llmcfg.Provider, error) {
	return s.db.ListProviders(ctx, onlyEnabled)
}

// ListRoleRoutes 直穿底层 store。
func (s *Store) ListRoleRoutes(ctx context.Context) ([]llmcfg.RoleRoute, error) {
	return s.db.ListRoleRoutes(ctx)
}

// ── 写（前端 CRUD 走这里，保证跨进程一致）─────────────────────────────
//
// 每个写方法：写 DB → cachestore.Invalidate（本进程即时清 L1+L2 + 广播失效键给其它进程）。

// SaveProvider upsert 一个 provider（按 key：存在则更新、不存在则新建），失效其 provider 键。
// provider 的能力（vision/context_window）参与 role 解析后的取值，但不入 routing 快照，故仅失效 provider 键。
func (s *Store) SaveProvider(ctx context.Context, p llmcfg.ProviderParams) (llmcfg.Provider, error) {
	pr, err := s.db.UpdateProvider(ctx, p)
	if isNotFound(err) {
		pr, err = s.db.CreateProvider(ctx, p)
	}
	if err != nil {
		return llmcfg.Provider{}, err
	}
	if err := s.cache.Invalidate(ctx, keyProvider(pr.Key)); err != nil {
		return pr, err
	}
	return pr, nil
}

// DeleteProvider 按 key 删 provider，失效其 provider 键。
// 被角色路由 FK 引用（ON DELETE RESTRICT）时撞约束，错误透传给 handler 转 409。
func (s *Store) DeleteProvider(ctx context.Context, key string) error {
	if err := s.db.DeleteProvider(ctx, key); err != nil {
		return err
	}
	return s.cache.Invalidate(ctx, keyProvider(key))
}

// UpsertRoleRoute upsert 一个角色 → provider key 直连映射，失效路由快照。
func (s *Store) UpsertRoleRoute(ctx context.Context, role, providerKey string) (llmcfg.RoleRoute, error) {
	rr, err := s.db.UpsertRoleRoute(ctx, role, providerKey)
	if err != nil {
		return llmcfg.RoleRoute{}, err
	}
	if err := s.cache.Invalidate(ctx, keyRouting); err != nil {
		return rr, err
	}
	return rr, nil
}

// DeleteRoleRoute 按 role 删角色路由，失效路由快照。
func (s *Store) DeleteRoleRoute(ctx context.Context, role string) error {
	if err := s.db.DeleteRoleRoute(ctx, role); err != nil {
		return err
	}
	return s.cache.Invalidate(ctx, keyRouting)
}
