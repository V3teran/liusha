// Package configstore 是 scenario/playbook/hunter 配置的多级缓存读写层：
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
	cfgplaybook "github.com/V3teran/liusha/internal/config/playbook"
	cfgscenario "github.com/V3teran/liusha/internal/config/scenario"
)

// 资源类型标签（失效消息 kind 字段 + 缓存键前缀）。
const (
	kindScenario = "scenario"
	kindPlaybook = "playbook"
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
}

// playbookStore 是 configstore 依赖的 playbook 底层能力（*cfgplaybook.Store 满足）。
type playbookStore interface {
	GetByID(ctx context.Context, id string) (cfgplaybook.Playbook, error)
	GetByCode(ctx context.Context, code string) (cfgplaybook.Playbook, error)
	Create(ctx context.Context, p cfgplaybook.NewParams) (cfgplaybook.Playbook, error)
	Update(ctx context.Context, p cfgplaybook.NewParams) (cfgplaybook.Playbook, error)
	Delete(ctx context.Context, code string) error
	List(ctx context.Context) ([]cfgplaybook.Playbook, error)
	SetHunters(ctx context.Context, playbookID string, items []cfgplaybook.PlaybookHunter) error
	ListHunters(ctx context.Context, playbookID string) ([]cfghunter.Hunter, error)
}

// hunterStore 是 configstore 依赖的 hunter 底层能力（*cfghunter.Store 满足）。
type hunterStore interface {
	GetByID(ctx context.Context, id string) (cfghunter.Hunter, error)
	GetByCode(ctx context.Context, code string) (cfghunter.Hunter, error)
	Create(ctx context.Context, p cfghunter.NewParams) (cfghunter.Hunter, error)
	Update(ctx context.Context, p cfghunter.NewParams) (cfghunter.Hunter, error)
	Delete(ctx context.Context, code string) error
	List(ctx context.Context, onlyEnabled bool) ([]cfghunter.Hunter, error)
	GetOrchestrator(ctx context.Context) (cfghunter.Hunter, error)
}

// Store 编排多级读写：底层 DB store + redis(L2/总线) + 进程内 L1。
type Store struct {
	scenarios scenarioStore
	playbooks playbookStore
	hunters   hunterStore
	rdb       *redis.Client
	l1        *l1Cache
}

// New 用 pgxpool + redis 客户端构造多级缓存 Store（生产装配用）。
func New(pool *pgxpool.Pool, rdb *redis.Client) *Store {
	return newWithStores(
		cfgscenario.NewStore(pool),
		cfgplaybook.NewStore(pool),
		cfghunter.NewStore(pool),
		rdb,
	)
}

// newWithStores 用已构造的底层 store 装配（测试注入 mock 用）。
func newWithStores(sc scenarioStore, pb playbookStore, hn hunterStore, rdb *redis.Client) *Store {
	return &Store{scenarios: sc, playbooks: pb, hunters: hn, rdb: rdb, l1: newL1()}
}

// ── 缓存键（L1/L2 同键，统一前缀 configstore:）───────────────────────────

const keyOrchestrator = "configstore:hunter:orchestrator"

func keyScenarioCode(code string) string  { return "configstore:scenario:code:" + code }
func keyScenarioID(id string) string      { return "configstore:scenario:id:" + id }
func keyPlaybookID(id string) string      { return "configstore:playbook:id:" + id }
func keyHunterID(id string) string        { return "configstore:hunter:id:" + id }
func keyPlaybookHunters(id string) string { return "configstore:playbook_hunters:" + id }

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

// PlaybookByID 按 uuid 读剧本（派发经 scenario.playbook_id FK；无 by-code 消费者）。
func (s *Store) PlaybookByID(ctx context.Context, id string) (cfgplaybook.Playbook, error) {
	return readThrough(ctx, s, keyPlaybookID(id),
		func(pb cfgplaybook.Playbook) []string { return []string{keyPlaybookID(pb.ID)} },
		func(ctx context.Context) (cfgplaybook.Playbook, error) {
			return s.playbooks.GetByID(ctx, id)
		})
}

// HunterByID 按 uuid 读猎手（CRUD :id；无 by-code 消费者）。
func (s *Store) HunterByID(ctx context.Context, id string) (cfghunter.Hunter, error) {
	return readThrough(ctx, s, keyHunterID(id),
		func(h cfghunter.Hunter) []string { return []string{keyHunterID(h.ID)} },
		func(ctx context.Context) (cfghunter.Hunter, error) {
			return s.hunters.GetByID(ctx, id)
		})
}

// PlaybookHunters 按 position 有序返回剧本内 domain 猎手（solo 拼 body / swarm 列子代理）。
func (s *Store) PlaybookHunters(ctx context.Context, playbookID string) ([]cfghunter.Hunter, error) {
	return readThrough(ctx, s, keyPlaybookHunters(playbookID),
		func([]cfghunter.Hunter) []string { return []string{keyPlaybookHunters(playbookID)} },
		func(ctx context.Context) ([]cfghunter.Hunter, error) {
			return s.playbooks.ListHunters(ctx, playbookID)
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

// ListPlaybooks 直穿底层 store。
func (s *Store) ListPlaybooks(ctx context.Context) ([]cfgplaybook.Playbook, error) {
	return s.playbooks.List(ctx)
}

// ListHunters 直穿底层 store。
func (s *Store) ListHunters(ctx context.Context, onlyEnabled bool) ([]cfghunter.Hunter, error) {
	return s.hunters.List(ctx, onlyEnabled)
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

// SavePlaybook upsert 一个剧本（按 code），失效其 id 键。组合关系另经 SetHunters。
func (s *Store) SavePlaybook(ctx context.Context, p cfgplaybook.NewParams) (cfgplaybook.Playbook, error) {
	pb, err := s.playbooks.Update(ctx, p)
	if isNotFound(err) {
		pb, err = s.playbooks.Create(ctx, p)
	}
	if err != nil {
		return cfgplaybook.Playbook{}, err
	}
	if err := s.invalidate(ctx, invalidation{Kind: kindPlaybook, ID: pb.ID}); err != nil {
		return pb, err
	}
	return pb, nil
}

// SaveHunter upsert 一个猎手（按 code），失效其 id 键；并**总是**失效编排哨兵键
// （猎手可能被提/降为 orchestrator，一律清哨兵最省心且正确）。
func (s *Store) SaveHunter(ctx context.Context, p cfghunter.NewParams) (cfghunter.Hunter, error) {
	h, err := s.hunters.Update(ctx, p)
	if isNotFound(err) {
		h, err = s.hunters.Create(ctx, p)
	}
	if err != nil {
		return cfghunter.Hunter{}, err
	}
	if err := s.invalidate(ctx, invalidation{Kind: kindHunter, ID: h.ID, Orchestrator: true}); err != nil {
		return h, err
	}
	return h, nil
}

// SetHunters 事务重设剧本组合，失效该剧本的 playbook_hunters 派生键。
func (s *Store) SetHunters(ctx context.Context, playbookID string, items []cfgplaybook.PlaybookHunter) error {
	if err := s.playbooks.SetHunters(ctx, playbookID, items); err != nil {
		return err
	}
	return s.invalidate(ctx, invalidation{Kind: kindPlaybook, PlaybookID: playbookID})
}

// DeleteScenario 按 code 删场景，失效 code+id 两张映射。
func (s *Store) DeleteScenario(ctx context.Context, id, code string) error {
	if err := s.scenarios.Delete(ctx, code); err != nil {
		return err
	}
	return s.invalidate(ctx, invalidation{Kind: kindScenario, ID: id, Code: code})
}

// DeletePlaybook 按 code 删剧本（组合随 CASCADE 清），失效其 id 键 + 组合派生键。
// 被 scenario 引用时撞 DB ON DELETE RESTRICT，错误透传给 handler 转 409。
func (s *Store) DeletePlaybook(ctx context.Context, id, code string) error {
	if err := s.playbooks.Delete(ctx, code); err != nil {
		return err
	}
	return s.invalidate(ctx, invalidation{Kind: kindPlaybook, ID: id, PlaybookID: id})
}

// DeleteHunter 按 code 删猎手，失效其 id 键 + 编排哨兵键。
// 被 playbook_hunter 引用时撞 DB ON DELETE RESTRICT，错误透传给 handler 转 409。
func (s *Store) DeleteHunter(ctx context.Context, id, code string) error {
	if err := s.hunters.Delete(ctx, code); err != nil {
		return err
	}
	return s.invalidate(ctx, invalidation{Kind: kindHunter, ID: id, Orchestrator: true})
}
