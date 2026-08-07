// Package configstore 是 scenario/hunter 配置的多级缓存读写层，构建在资源无关的
// cachestore 内核之上：内存 L1（本进程）→ redis L2（跨进程共享 + 失效总线）→ DB（事实源）。
//
// 为何分层（见 D7）：api 与 runner 是**多进程**。前端在 api 改配置后，runner 的本地
// L1 必须被动失效，否则 runner 用旧配置装配。故写路径写 DB 后经 cachestore 广播失效键，
// 各进程共享的 cachestore.Subscribe goroutine 收到即清本地 L1 + L2，下次读回填最新值。
//
// 本包只贡献 scenario/hunter 专有的缓存键与读写方法，缓存机制（L1/L2/总线）全在 cachestore。
//
// 缓存粒度：仅**单条读**走 L1/L2 缓存；列表读低频（仅 admin CRUD 列表页）且失效成本高
// （任一成员变动都要废整表），故直穿 DB 不缓存。
package configstore

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/V3teran/liusha/internal/cachestore"
	cfghunter "github.com/V3teran/liusha/internal/config/hunter"
	cfgscenario "github.com/V3teran/liusha/internal/config/scenario"
)

// scenarioStore 是 configstore 依赖的 scenario 底层能力（*cfgscenario.Store 满足）。
type scenarioStore interface {
	GetByCode(ctx context.Context, code string) (cfgscenario.Scenario, error)
	GetByID(ctx context.Context, id string) (cfgscenario.Scenario, error)
	Create(ctx context.Context, p cfgscenario.NewParams) (cfgscenario.Scenario, error)
	Update(ctx context.Context, p cfgscenario.NewParams) (cfgscenario.Scenario, error)
	Delete(ctx context.Context, code string) error
	List(ctx context.Context, onlyEnabled bool) ([]cfgscenario.Scenario, error)
	ListPaged(ctx context.Context, p cfgscenario.ListParams) ([]cfgscenario.Scenario, error)
	Count(ctx context.Context, p cfgscenario.ListParams) (int, error)
}

// hunterStore 是 configstore 依赖的 hunter 底层能力（*cfghunter.Store 满足）。
type hunterStore interface {
	GetByID(ctx context.Context, id string) (cfghunter.Hunter, error)
	GetByCode(ctx context.Context, code string) (cfghunter.Hunter, error)
	Create(ctx context.Context, p cfghunter.NewParams) (cfghunter.Hunter, error)
	Update(ctx context.Context, p cfghunter.NewParams) (cfghunter.Hunter, error)
	Delete(ctx context.Context, code string) error
	List(ctx context.Context, onlyEnabled bool) ([]cfghunter.Hunter, error)
	ListPaged(ctx context.Context, p cfghunter.ListParams) ([]cfghunter.Hunter, error)
	CountList(ctx context.Context, p cfghunter.ListParams) (int, error)
	ListEnabledDomain(ctx context.Context) ([]cfghunter.Hunter, error)
	GetOrchestrator(ctx context.Context) (cfghunter.Hunter, error)
}

// Store 编排 scenario/hunter 的多级读写：底层 DB store + 共享 cachestore 内核。
type Store struct {
	scenarios scenarioStore
	hunters   hunterStore
	cache     *cachestore.Cache
}

// New 用 pgxpool + 共享 cachestore 构造 Store（生产装配用）。
// cache 由进程唯一构造并已 go cache.Subscribe(ctx)，可被多个资源仓储共享。
func New(pool *pgxpool.Pool, cache *cachestore.Cache) *Store {
	return newWithStores(
		cfgscenario.NewStore(pool),
		cfghunter.NewStore(pool),
		cache,
	)
}

// newWithStores 用已构造的底层 store 装配（测试注入 mock 用）。
func newWithStores(sc scenarioStore, hn hunterStore, cache *cachestore.Cache) *Store {
	return &Store{scenarios: sc, hunters: hn, cache: cache}
}

// ── 缓存键（L1/L2 同键，统一前缀 configstore:）───────────────────────────

// keyOrchestrator / keyEnabledDomain 是两个哨兵键（无参），分别缓存全局唯一编排猎手
// 与 swarm 的 enabled 领域池。任一 hunter 存/删即失效二者（见 SaveHunter/DeleteHunter）。
const (
	keyOrchestrator  = "configstore:hunter:orchestrator"
	keyEnabledDomain = "configstore:hunters:enabled_domain"
)

func keyScenarioCode(code string) string { return "configstore:scenario:code:" + code }
func keyScenarioID(id string) string     { return "configstore:scenario:id:" + id }
func keyHunterID(id string) string       { return "configstore:hunter:id:" + id }

// ── 小工具 ────────────────────────────────────────────────────────────

// isNotFound 判定底层 store 的「不存在」——三个 store 的 GetByCode 均用 %w 包 pgx.ErrNoRows，
// upsert 路径据此在 Update 落空时回退 Create。
func isNotFound(err error) bool { return err != nil && errors.Is(err, pgx.ErrNoRows) }

// ── 单条读（L1/L2 缓存）───────────────────────────────────────────────

// ScenarioByCode 走 code 路读场景（运行期派发热路径——task.scenario_id 存 code，见 D3）。
func (s *Store) ScenarioByCode(ctx context.Context, code string) (cfgscenario.Scenario, error) {
	return cachestore.ReadThrough(ctx, s.cache, keyScenarioCode(code),
		func(sc cfgscenario.Scenario) []string {
			return []string{keyScenarioCode(sc.Code), keyScenarioID(sc.ID)}
		},
		func(ctx context.Context) (cfgscenario.Scenario, error) {
			return s.scenarios.GetByCode(ctx, code)
		})
}

// ScenarioByID 走 id 路读场景（admin CRUD :id 用）。与 code 路命中同一份值。
func (s *Store) ScenarioByID(ctx context.Context, id string) (cfgscenario.Scenario, error) {
	return cachestore.ReadThrough(ctx, s.cache, keyScenarioID(id),
		func(sc cfgscenario.Scenario) []string {
			return []string{keyScenarioCode(sc.Code), keyScenarioID(sc.ID)}
		},
		func(ctx context.Context) (cfgscenario.Scenario, error) {
			return s.scenarios.GetByID(ctx, id)
		})
}

// HunterByID 按 uuid 读猎手（CRUD :id；solo 派发经 scenario.solo_hunter_id）。
func (s *Store) HunterByID(ctx context.Context, id string) (cfghunter.Hunter, error) {
	return cachestore.ReadThrough(ctx, s.cache, keyHunterID(id),
		func(h cfghunter.Hunter) []string { return []string{keyHunterID(h.ID)} },
		func(ctx context.Context) (cfghunter.Hunter, error) {
			return s.hunters.GetByID(ctx, id)
		})
}

// HunterByCode 按 code 读猎手，直穿底层 store（不缓存）：hunter 缓存只建 id 键，
// SaveHunter 也只失效 id+哨兵；若在此缓存 code 键，SaveHunter 后会 stale。工具装配
// 写路径按 code 取完整猎手再改数组回存，直读最新即可，无需缓存。
func (s *Store) HunterByCode(ctx context.Context, code string) (cfghunter.Hunter, error) {
	return s.hunters.GetByCode(ctx, code)
}

// EnabledDomainHunters 返回全部 enabled 领域猎手（swarm 子代理池），缓存于哨兵键。
func (s *Store) EnabledDomainHunters(ctx context.Context) ([]cfghunter.Hunter, error) {
	return cachestore.ReadThrough(ctx, s.cache, keyEnabledDomain,
		func([]cfghunter.Hunter) []string { return []string{keyEnabledDomain} },
		func(ctx context.Context) ([]cfghunter.Hunter, error) {
			return s.hunters.ListEnabledDomain(ctx)
		})
}

// Orchestrator 取全局唯一编排猎手（kind='orchestrator' AND enabled，见 D1），缓存于哨兵键。
func (s *Store) Orchestrator(ctx context.Context) (cfghunter.Hunter, error) {
	return cachestore.ReadThrough(ctx, s.cache, keyOrchestrator,
		func(cfghunter.Hunter) []string { return []string{keyOrchestrator} },
		func(ctx context.Context) (cfghunter.Hunter, error) {
			return s.hunters.GetOrchestrator(ctx)
		})
}

// ── 列表读（不缓存，直穿 DB）──────────────────────────────────────────

// ListScenarios 直穿底层 store（admin 列表页低频，不落缓存）。
func (s *Store) ListScenarios(ctx context.Context, onlyEnabled bool) ([]cfgscenario.Scenario, error) {
	return s.scenarios.List(ctx, onlyEnabled)
}

// ListHunters 直穿底层 store。
func (s *Store) ListHunters(ctx context.Context, onlyEnabled bool) ([]cfghunter.Hunter, error) {
	return s.hunters.List(ctx, onlyEnabled)
}

// ListScenariosPaged 直穿底层 store：搜索 + 分页（配置管理页）。
func (s *Store) ListScenariosPaged(ctx context.Context, p cfgscenario.ListParams) ([]cfgscenario.Scenario, error) {
	return s.scenarios.ListPaged(ctx, p)
}

// CountScenarios 直穿底层 store：与 ListScenariosPaged 同过滤的总数。
func (s *Store) CountScenarios(ctx context.Context, p cfgscenario.ListParams) (int, error) {
	return s.scenarios.Count(ctx, p)
}

// ListHuntersPaged 直穿底层 store：搜索 + 分页（配置管理页）。
func (s *Store) ListHuntersPaged(ctx context.Context, p cfghunter.ListParams) ([]cfghunter.Hunter, error) {
	return s.hunters.ListPaged(ctx, p)
}

// CountHunters 直穿底层 store：与 ListHuntersPaged 同过滤的总数。
func (s *Store) CountHunters(ctx context.Context, p cfghunter.ListParams) (int, error) {
	return s.hunters.CountList(ctx, p)
}

// ── 写（前端 CRUD 走这里，保证跨进程一致）─────────────────────────────
//
// 每个写方法：写 DB → cachestore.Invalidate（本进程即时清 L1+L2 + 广播失效键给其它进程）。
// 失效的键由写方直接列出（与 ReadThrough 的 fillKeys 对应），无 per-resource 语义 switch。

// scenarioKeys 是一条场景占用的全部缓存键（code + id 双映射，指向同一份值）。
func scenarioKeys(id, code string) []string {
	return []string{keyScenarioCode(code), keyScenarioID(id)}
}

// hunterKeys 是一次猎手写/删要清的全部缓存键：其 id 键 + 两个哨兵键
// （猎手可能被提/降为 orchestrator，或 enabled/kind 变动影响领域池，一律清哨兵最省心且正确）。
func hunterKeys(id string) []string {
	return []string{keyHunterID(id), keyOrchestrator, keyEnabledDomain}
}

// SaveScenario upsert 一个场景（按 code：存在则更新、不存在则新建），失效 code+id 两张映射。
func (s *Store) SaveScenario(ctx context.Context, p cfgscenario.NewParams) (cfgscenario.Scenario, error) {
	sc, err := s.scenarios.Update(ctx, p)
	if isNotFound(err) {
		sc, err = s.scenarios.Create(ctx, p)
	}
	if err != nil {
		return cfgscenario.Scenario{}, err
	}
	if err := s.cache.Invalidate(ctx, scenarioKeys(sc.ID, sc.Code)...); err != nil {
		return sc, err
	}
	return sc, nil
}

// SaveHunter upsert 一个猎手（按 code），失效其 id 键 + 两个哨兵键。
func (s *Store) SaveHunter(ctx context.Context, p cfghunter.NewParams) (cfghunter.Hunter, error) {
	h, err := s.hunters.Update(ctx, p)
	if isNotFound(err) {
		h, err = s.hunters.Create(ctx, p)
	}
	if err != nil {
		return cfghunter.Hunter{}, err
	}
	if err := s.cache.Invalidate(ctx, hunterKeys(h.ID)...); err != nil {
		return h, err
	}
	return h, nil
}

// DeleteScenario 按 code 删场景，失效 code+id 两张映射。
func (s *Store) DeleteScenario(ctx context.Context, id, code string) error {
	if err := s.scenarios.Delete(ctx, code); err != nil {
		return err
	}
	return s.cache.Invalidate(ctx, scenarioKeys(id, code)...)
}

// DeleteHunter 按 code 删猎手，失效其 id 键 + 两个哨兵键。
// 被 scenario.solo_hunter_id 引用时撞 DB ON DELETE RESTRICT，错误透传给 handler 转 409。
func (s *Store) DeleteHunter(ctx context.Context, id, code string) error {
	if err := s.hunters.Delete(ctx, code); err != nil {
		return err
	}
	return s.cache.Invalidate(ctx, hunterKeys(id)...)
}
