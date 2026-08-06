// Package configstore 是 scenario/hunter 配置的多级缓存读写层：
// 内存 L1（本进程）→ redis L2（跨进程共享 + 失效总线）→ DB（事实源）。
//
// 为何分层（见 D7）：api 与 runner 是**多进程**。前端在 api 改配置后，runner 的本地
// L1 必须被动失效，否则 runner 用旧配置装配。故写路径写 DB 后经 redis PUBLISH 广播失效，
// 各进程 Subscribe goroutine 收到即清本地 L1 + L2，下次读回填最新值。
//
// 缓存粒度：仅**单条读**走 L1/L2 缓存；列表读低频（仅 admin CRUD 列表页）且失效成本高
// （任一成员变动都要废整表），故直穿 DB 不缓存。
package configstore

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	cfghunter "github.com/V3teran/liusha/internal/config/hunter"
	cfgscenario "github.com/V3teran/liusha/internal/config/scenario"
)

// 资源类型标签（失效消息 kind 字段 + 缓存键前缀）。
const (
	kindScenario = "scenario"
	kindHunter   = "hunter"
)

// l2TTL 是 redis L2 条目的保守兜底过期（防订阅漏消息导致的永久脏读）。
// 正常失效靠 PUBLISH 驱逐，TTL 只是安全网。
const l2TTL = 10 * time.Minute

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

// Store 编排多级读写：底层 DB store + redis(L2/总线) + 进程内 L1。
type Store struct {
	scenarios scenarioStore
	hunters   hunterStore
	rdb       *redis.Client
	l1        *l1Cache
}

// New 用 pgxpool + redis 客户端构造多级缓存 Store（生产装配用）。
func New(pool *pgxpool.Pool, rdb *redis.Client) *Store {
	return newWithStores(
		cfgscenario.NewStore(pool),
		cfghunter.NewStore(pool),
		rdb,
	)
}

// newWithStores 用已构造的底层 store 装配（测试注入 mock 用）。
func newWithStores(sc scenarioStore, hn hunterStore, rdb *redis.Client) *Store {
	return &Store{scenarios: sc, hunters: hn, rdb: rdb, l1: newL1()}
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

// jsonMarshal / jsonUnmarshal 复用标准库，单独包一层便于统一缓存编解码点。
func jsonMarshal(v any) ([]byte, error)   { return json.Marshal(v) }
func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

// isNotFound 判定底层 store 的「不存在」——三个 store 的 GetByCode 均用 %w 包 pgx.ErrNoRows，
// upsert 路径据此在 Update 落空时回退 Create。
func isNotFound(err error) bool { return err != nil && errors.Is(err, pgx.ErrNoRows) }

// readThrough 是多级读的统一泛型骨架：L1 命中即返 → L2（同键）命中回填 L1 → DB
// 回填 L1+L2。miss 时经 load 打 DB，把结果 json 缓存到两级。fillKeys 返回该值应写入
// 的全部缓存键（scenario 双键指向同值，其余单键）。
func readThrough[T any](
	ctx context.Context, s *Store, primaryKey string,
	fillKeys func(T) []string, load func(context.Context) (T, error),
) (T, error) {
	var zero T
	// L1
	if b, ok := s.l1.get(primaryKey); ok {
		return decode[T](b, zero)
	}
	// L2
	if b, err := s.rdb.Get(ctx, primaryKey).Bytes(); err == nil {
		s.l1.set(primaryKey, b) // 回填 L1
		return decode[T](b, zero)
	}
	// DB
	v, err := load(ctx)
	if err != nil {
		return zero, err
	}
	s.fill(ctx, v, fillKeys(v))
	return v, nil
}

// decode 把缓存字节反序列化为 T；失败返回 zero + err。
func decode[T any](b []byte, zero T) (T, error) {
	var v T
	if err := jsonUnmarshal(b, &v); err != nil {
		return zero, err
	}
	return v, nil
}

// fill 把值 json 编码后写入给定的全部 L1+L2 键（L2 带兜底 TTL）。
func (s *Store) fill(ctx context.Context, v any, keys []string) {
	b, err := jsonMarshal(v)
	if err != nil {
		return // 缓存失败不致命，下次读重试
	}
	for _, k := range keys {
		s.l1.set(k, b)
		_ = s.rdb.Set(ctx, k, b, l2TTL).Err()
	}
}

// ── 单条读（L1/L2 缓存）───────────────────────────────────────────────

// ScenarioByCode 走 code 路读场景（运行期派发热路径——task.scenario_id 存 code，见 D3）。
func (s *Store) ScenarioByCode(ctx context.Context, code string) (cfgscenario.Scenario, error) {
	return readThrough(ctx, s, keyScenarioCode(code),
		func(sc cfgscenario.Scenario) []string {
			return []string{keyScenarioCode(sc.Code), keyScenarioID(sc.ID)}
		},
		func(ctx context.Context) (cfgscenario.Scenario, error) {
			return s.scenarios.GetByCode(ctx, code)
		})
}

// ScenarioByID 走 id 路读场景（admin CRUD :id 用）。与 code 路命中同一份值。
func (s *Store) ScenarioByID(ctx context.Context, id string) (cfgscenario.Scenario, error) {
	return readThrough(ctx, s, keyScenarioID(id),
		func(sc cfgscenario.Scenario) []string {
			return []string{keyScenarioCode(sc.Code), keyScenarioID(sc.ID)}
		},
		func(ctx context.Context) (cfgscenario.Scenario, error) {
			return s.scenarios.GetByID(ctx, id)
		})
}

// HunterByID 按 uuid 读猎手（CRUD :id；solo 派发经 scenario.solo_hunter_id）。
func (s *Store) HunterByID(ctx context.Context, id string) (cfghunter.Hunter, error) {
	return readThrough(ctx, s, keyHunterID(id),
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
	return readThrough(ctx, s, keyEnabledDomain,
		func([]cfghunter.Hunter) []string { return []string{keyEnabledDomain} },
		func(ctx context.Context) ([]cfghunter.Hunter, error) {
			return s.hunters.ListEnabledDomain(ctx)
		})
}

// Orchestrator 取全局唯一编排猎手（kind='orchestrator' AND enabled，见 D1），缓存于哨兵键。
func (s *Store) Orchestrator(ctx context.Context) (cfghunter.Hunter, error) {
	return readThrough(ctx, s, keyOrchestrator,
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
// 每个写方法：写 DB → 本进程 evict（清 L1+L2）→ PUBLISH 广播失效（其它进程 evict）。
// 本进程立即 evict 而非等自己的订阅回环，避免写后瞬时读到脏值。

// invalidate 清本进程 L1+L2 并广播失效消息（本地即时 + 跨进程最终一致）。
func (s *Store) invalidate(ctx context.Context, msg invalidation) error {
	s.evict(ctx, msg)
	return s.publish(ctx, msg)
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
	if err := s.invalidate(ctx, invalidation{Kind: kindScenario, ID: sc.ID, Code: sc.Code}); err != nil {
		return sc, err
	}
	return sc, nil
}

// SaveHunter upsert 一个猎手（按 code），失效其 id 键；并**总是**失效两个哨兵键
// （猎手可能被提/降为 orchestrator，或 enabled/kind 变动影响领域池，一律清哨兵最省心且正确）。
func (s *Store) SaveHunter(ctx context.Context, p cfghunter.NewParams) (cfghunter.Hunter, error) {
	h, err := s.hunters.Update(ctx, p)
	if isNotFound(err) {
		h, err = s.hunters.Create(ctx, p)
	}
	if err != nil {
		return cfghunter.Hunter{}, err
	}
	if err := s.invalidate(ctx, invalidation{Kind: kindHunter, ID: h.ID, Sentinels: true}); err != nil {
		return h, err
	}
	return h, nil
}

// DeleteScenario 按 code 删场景，失效 code+id 两张映射。
func (s *Store) DeleteScenario(ctx context.Context, id, code string) error {
	if err := s.scenarios.Delete(ctx, code); err != nil {
		return err
	}
	return s.invalidate(ctx, invalidation{Kind: kindScenario, ID: id, Code: code})
}

// DeleteHunter 按 code 删猎手，失效其 id 键 + 两个哨兵键。
// 被 scenario.solo_hunter_id 引用时撞 DB ON DELETE RESTRICT，错误透传给 handler 转 409。
func (s *Store) DeleteHunter(ctx context.Context, id, code string) error {
	if err := s.hunters.Delete(ctx, code); err != nil {
		return err
	}
	return s.invalidate(ctx, invalidation{Kind: kindHunter, ID: id, Sentinels: true})
}
