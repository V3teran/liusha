package settingstore

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/V3teran/liusha/internal/cachestore"
)

// settingDB 是 settingstore 依赖的底层持久化能力（*dbStore 自动满足）。
type settingDB interface {
	GetCompaction(ctx context.Context) (CompactionSettings, error)
	SaveCompaction(ctx context.Context, v CompactionSettings) error
	GetRuntime(ctx context.Context) (RuntimeSettings, error)
	SaveRuntime(ctx context.Context, v RuntimeSettings) error
	GetProxyFilter(ctx context.Context) (ProxyFilterSettings, error)
	SaveProxyFilter(ctx context.Context, v ProxyFilterSettings) error
}

// Store 编排系统业务旋钮的多级读写：底层 DB store + 共享 cachestore 内核。
type Store struct {
	db    settingDB
	cache *cachestore.Cache
}

// New 用 pgxpool + 共享 cachestore 构造 Store（生产装配用）。
// cache 由进程唯一构造并已 go cache.Subscribe(ctx)，与 llmstore 等复用同一实例。
func New(pool *pgxpool.Pool, cache *cachestore.Cache) *Store {
	return newWithStore(newDBStore(pool), cache)
}

// newWithStore 用已构造的底层 store 装配（测试注入 mock 用）。
func newWithStore(db settingDB, cache *cachestore.Cache) *Store {
	return &Store{db: db, cache: cache}
}

// ── 缓存键（L1/L2 同键，统一前缀 settingstore:）────────────────────────
//
// 三组各一个哨兵键：该组任一字段写即失效对应键（见 SaveCompaction/SaveRuntime/SaveProxyFilter）。
const (
	keyCompaction  = "settingstore:compaction"
	keyRuntime     = "settingstore:runtime"
	keyProxyFilter = "settingstore:proxy_filter"
)

// KeyProxyFilter 导出 proxy_filter 组的缓存键：cmd/proxy 的失效回调据此判定本次失效是否
// 涉及过滤规则（涉及才重建过滤链原子换入），无需订阅方感知其它组。
const KeyProxyFilter = keyProxyFilter

// ── 读（运行期热路径，走 L1/L2 缓存）──────────────────────────────────

// Compaction 读会话历史压缩旋钮快照（runner 每 task 现读，命中多级缓存）。
func (s *Store) Compaction(ctx context.Context) (CompactionSettings, error) {
	return cachestore.ReadThrough(ctx, s.cache, keyCompaction,
		func(CompactionSettings) []string { return []string{keyCompaction} },
		func(ctx context.Context) (CompactionSettings, error) {
			return s.db.GetCompaction(ctx)
		})
}

// Runtime 读工具运行时旋钮快照（工具超时 / 截尾 / prompt finding 上限现读）。
func (s *Store) Runtime(ctx context.Context) (RuntimeSettings, error) {
	return cachestore.ReadThrough(ctx, s.cache, keyRuntime,
		func(RuntimeSettings) []string { return []string{keyRuntime} },
		func(ctx context.Context) (RuntimeSettings, error) {
			return s.db.GetRuntime(ctx)
		})
}

// ProxyFilter 读代理流量过滤规则快照（proxy 失效后据此重建过滤链原子替换）。
func (s *Store) ProxyFilter(ctx context.Context) (ProxyFilterSettings, error) {
	return cachestore.ReadThrough(ctx, s.cache, keyProxyFilter,
		func(ProxyFilterSettings) []string { return []string{keyProxyFilter} },
		func(ctx context.Context) (ProxyFilterSettings, error) {
			return s.db.GetProxyFilter(ctx)
		})
}

// ── 写（前端 CRUD 走这里，保证跨进程一致）───────────────────────────
//
// 每个写方法：写 DB → cachestore.Invalidate（本进程即时清 L1+L2 + 广播失效键给其它进程）。

// SaveCompaction 覆写会话历史压缩旋钮，失效其快照键。
func (s *Store) SaveCompaction(ctx context.Context, v CompactionSettings) error {
	if err := s.db.SaveCompaction(ctx, v); err != nil {
		return err
	}
	return s.cache.Invalidate(ctx, keyCompaction)
}

// SaveRuntime 覆写工具运行时旋钮，失效其快照键。
func (s *Store) SaveRuntime(ctx context.Context, v RuntimeSettings) error {
	if err := s.db.SaveRuntime(ctx, v); err != nil {
		return err
	}
	return s.cache.Invalidate(ctx, keyRuntime)
}

// SaveProxyFilter 覆写代理流量过滤规则，失效其快照键（proxy 侧收到即热换过滤链）。
func (s *Store) SaveProxyFilter(ctx context.Context, v ProxyFilterSettings) error {
	if err := s.db.SaveProxyFilter(ctx, v); err != nil {
		return err
	}
	return s.cache.Invalidate(ctx, keyProxyFilter)
}

// ── 种子（insert-only，缺组才写；见 seed 包）────────────────────────

// GetCompactionRaw / GetRuntimeRaw / GetProxyFilterRaw 直穿底层 store（种子判存与否用，绕缓存）。
// isNotFound(err) 为真表示该组缺行。
func (s *Store) GetCompactionRaw(ctx context.Context) (CompactionSettings, error) {
	return s.db.GetCompaction(ctx)
}
func (s *Store) GetRuntimeRaw(ctx context.Context) (RuntimeSettings, error) {
	return s.db.GetRuntime(ctx)
}
func (s *Store) GetProxyFilterRaw(ctx context.Context) (ProxyFilterSettings, error) {
	return s.db.GetProxyFilter(ctx)
}

// IsNotFound 导出缺组判定给种子/上层用。
func IsNotFound(err error) bool { return isNotFound(err) }
