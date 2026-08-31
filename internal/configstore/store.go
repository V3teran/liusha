// Package configstore 是 scenario/agent 配置的多级缓存读写层，构建在资源无关的
// cachestore 内核之上：内存 L1（本进程）→ redis L2（跨进程共享 + 失效总线）→ DB（事实源）。
//
// 为何分层（见 D7）：api 与 runner 是**多进程**。前端在 api 改配置后，runner 的本地
// L1 必须被动失效，否则 runner 用旧配置装配。故写路径写 DB 后经 cachestore 广播失效键，
// 各进程共享的 cachestore.Subscribe goroutine 收到即清本地 L1 + L2，下次读回填最新值。
//
// 本包只贡献 scenario/agent 专有的缓存键与读写方法，缓存机制（L1/L2/总线）全在 cachestore。
//
// 缓存粒度：单条读 + **key 空间固定**的读均走 L1/L2 缓存——单条读（id/code/tier）、
// 全量列表读（ListExecutors/ListScenarios 按 onlyEnabled 分 2 键）、两个哨兵（planner/
// enabled_domain）。任一成员写即失效对应固定键。唯**分页/搜索列表**（ListExecutorsPaged/
// ListScenariosPaged + Count）不缓存：其键含搜索词 × limit × offset，key 空间随查询无限
// 增长，L1 无 TTL 会堆积孤儿键（内存泄漏），且配置管理页低频，缓存收益近零，故直穿 DB。
package configstore

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/V3teran/liusha/internal/cachestore"
	cfgagent "github.com/V3teran/liusha/internal/config/agent"
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

// executorStore 是 configstore 依赖的 agent 底层能力（*cfgagent.Store 满足）。
type executorStore interface {
	GetByID(ctx context.Context, id string) (cfgagent.Agent, error)
	GetByCode(ctx context.Context, code string) (cfgagent.Agent, error)
	GetPlanner(ctx context.Context) (cfgagent.Agent, error)
	GetExecutor(ctx context.Context) (cfgagent.Agent, error)
	Update(ctx context.Context, code string, p cfgagent.UpdateParams) (cfgagent.Agent, error)
	UpdateComplexity(ctx context.Context, id, complexity string) (cfgagent.Agent, error)
	List(ctx context.Context, onlyEnabled bool) ([]cfgagent.Agent, error)
	ListPaged(ctx context.Context, p cfgagent.ListParams) ([]cfgagent.Agent, error)
	CountList(ctx context.Context, p cfgagent.ListParams) (int, error)
	ComplexityByCode(ctx context.Context, code string) (complexity string, found bool, err error)
}

// Store 编排 scenario/agent 的多级读写：底层 DB store + 共享 cachestore 内核。
type Store struct {
	scenarios scenarioStore
	executors   executorStore
	cache     *cachestore.Cache
}

// New 用 pgxpool + 共享 cachestore 构造 Store（生产装配用）。
// cache 由进程唯一构造并已 go cache.Subscribe(ctx)，可被多个资源仓储共享。
func New(pool *pgxpool.Pool, cache *cachestore.Cache) *Store {
	return newWithStores(
		cfgscenario.NewStore(pool),
		cfgagent.NewStore(pool),
		cache,
	)
}

// newWithStores 用已构造的底层 store 装配（测试注入 mock 用）。
func newWithStores(sc scenarioStore, hn executorStore, cache *cachestore.Cache) *Store {
	return &Store{scenarios: sc, executors: hn, cache: cache}
}

// ── 缓存键（L1/L2 同键，统一前缀 configstore:）───────────────────────────

// keyplanner / keyExecutor 是两个哨兵键（无参），分别缓存全局唯一的 Planner 和 Executor
// 与 swarm 的 enabled 领域池。任一 agent 存/删即失效二者（见 SaveExecutor/DeleteExecutor）。
const (
	keyplanner  = "configstore:executor:planner"
	keyExecutor = "configstore:executor:executor"
)

func keyScenarioCode(code string) string { return "configstore:scenario:code:" + code }
func keyScenarioID(id string) string     { return "configstore:scenario:id:" + id }
func keyExecutorID(id string) string       { return "configstore:executor:id:" + id }
func keyExecutorComplexity(code string) string { return "configstore:executor:complexity:code:" + code }

// keyAgentsList / keyScenariosList 是全量列表读的缓存键，按 onlyEnabled 分两键（有界）。
// 任一 agent/scenario 写即失效其资源的两个 list 键（enabled 变动会跨 true/false 两表）。
func keyAgentsList(onlyEnabled bool) string {
	return "configstore:executors: list:" + boolKey(onlyEnabled)
}
func keyScenariosList(onlyEnabled bool) string {
	return "configstore:scenarios:list:" + boolKey(onlyEnabled)
}

// boolKey 把 onlyEnabled 稳定映射为键后缀。
func boolKey(b bool) string {
	if b {
		return "enabled"
	}
	return "all"
}

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

// ExecutorByID 按 uuid 读操作员（CRUD :id；solo 派发经 scenario.solo_agent_id）。
func (s *Store) ExecutorByID(ctx context.Context, id string) (cfgagent.Agent, error) {
	return cachestore.ReadThrough(ctx, s.cache, keyExecutorID(id),
		func(h cfgagent.Agent) []string { return []string{keyExecutorID(h.ID)} },
		func(ctx context.Context) (cfgagent.Agent, error) {
			return s.executors.GetByID(ctx, id)
		})
}

// ExecutorByCode 按 code 读操作员，直穿底层 store（不缓存）：agent 缓存只建 id 键，
// SaveExecutor 也只失效 id+哨兵；若在此缓存 code 键，SaveExecutor 后会 stale。工具装配
// 写路径按 code 取完整操作员再改数组回存，直读最新即可，无需缓存。
func (s *Store) ExecutorByCode(ctx context.Context, code string) (cfgagent.Agent, error) {
	return s.executors.GetByCode(ctx, code)
}

// complexityResult 是 ComplexityByCode 的缓存载体：连 found=false（非 agent 的路由 key）
// 也缓存，省掉这类 key 每次 agent-run 的重复 DB miss。
type complexityResult struct {
	Complexity string `json:"complexity"`
	Found      bool   `json:"found"`
}

// ComplexityByCode 读某 agent 的复杂度档位（LLM 路由第一跳 role→complexity 的 DB 覆盖，热路径：每次
// For(role) 解析都查）。走多级缓存于 complexity 键——两个写入口（UpdateExecutorComplexity 整字段改档、
// SaveExecutor 整体保存）都经 agentKeys 失效此键，故移档/保存后 runner 下次即读新档，无脏读。
func (s *Store) ComplexityByCode(ctx context.Context, code string) (complexity string, found bool, err error) {
	res, err := cachestore.ReadThrough(ctx, s.cache, keyExecutorComplexity(code),
		func(complexityResult) []string { return []string{keyExecutorComplexity(code)} },
		func(ctx context.Context) (complexityResult, error) {
			c, ok, err := s.executors.ComplexityByCode(ctx, code)
			if err != nil {
				return complexityResult{}, err
			}
			return complexityResult{Complexity: c, Found: ok}, nil
		})
	if err != nil {
		return "", false, err
	}
	return res.Complexity, res.Found, nil
}

// EnabledDomainExecutors 返回全部 enabled 领域操作员（swarm 子代理池），缓存于哨兵键。
func (s *Store) EnabledDomainExecutors(ctx context.Context) ([]cfgagent.Agent, error) {
	return cachestore.ReadThrough(ctx, s.cache, keyExecutor,
		func([]cfgagent.Agent) []string { return []string{keyExecutor} },
		func(ctx context.Context) ([]cfgagent.Agent, error) {
			executor, err := s.executors.GetExecutor(ctx)
			if err != nil {
				return nil, err
			}
			return []cfgagent.Agent{executor}, nil
		})
}

// planner 取全局唯一编排操作员（kind='planner' AND enabled，见 D1），缓存于哨兵键。
func (s *Store) Planner(ctx context.Context) (cfgagent.Agent, error) {
	return cachestore.ReadThrough(ctx, s.cache, keyplanner,
		func(cfgagent.Agent) []string { return []string{keyplanner} },
		func(ctx context.Context) (cfgagent.Agent, error) {
			return s.executors.GetPlanner(ctx)
		})
}

// ── 全量列表读（L1/L2 缓存，按 onlyEnabled 分键）─────────────────────────

// ListScenarios 全量列表读，走多级缓存（按 onlyEnabled 分键）。任一场景写即失效两键。
func (s *Store) ListScenarios(ctx context.Context, onlyEnabled bool) ([]cfgscenario.Scenario, error) {
	return cachestore.ReadThrough(ctx, s.cache, keyScenariosList(onlyEnabled),
		func([]cfgscenario.Scenario) []string { return []string{keyScenariosList(onlyEnabled)} },
		func(ctx context.Context) ([]cfgscenario.Scenario, error) {
			return s.scenarios.List(ctx, onlyEnabled)
		})
}

// ListExecutors 全量列表读，走多级缓存（按 onlyEnabled 分键）。任一操作员写即失效两键。
func (s *Store) ListExecutors(ctx context.Context, onlyEnabled bool) ([]cfgagent.Agent, error) {
	return cachestore.ReadThrough(ctx, s.cache, keyAgentsList(onlyEnabled),
		func([]cfgagent.Agent) []string { return []string{keyAgentsList(onlyEnabled)} },
		func(ctx context.Context) ([]cfgagent.Agent, error) {
			return s.executors.List(ctx, onlyEnabled)
		})
}

// ── 分页/搜索列表读（不缓存，直穿 DB）─────────────────────────────────
//
// 键含搜索词 × limit × offset，key 空间随查询无限增长；L1 无 TTL 会堆积孤儿键（内存泄漏）。
// 配置管理页低频，缓存收益近零，故直穿 DB（与 liusha2 一致）。

// ListScenariosPaged 直穿底层 store：搜索 + 分页（配置管理页）。
func (s *Store) ListScenariosPaged(ctx context.Context, p cfgscenario.ListParams) ([]cfgscenario.Scenario, error) {
	return s.scenarios.ListPaged(ctx, p)
}

// CountScenarios 直穿底层 store：与 ListScenariosPaged 同过滤的总数。
func (s *Store) CountScenarios(ctx context.Context, p cfgscenario.ListParams) (int, error) {
	return s.scenarios.Count(ctx, p)
}

// ListExecutorsPaged 直穿底层 store：搜索 + 分页（配置管理页）。
func (s *Store) ListExecutorsPaged(ctx context.Context, p cfgagent.ListParams) ([]cfgagent.Agent, error) {
	return s.executors.ListPaged(ctx, p)
}

// CountExecutors 直穿底层 store：与 ListExecutorsPaged 同过滤的总数。
func (s *Store) CountExecutors(ctx context.Context, p cfgagent.ListParams) (int, error) {
	return s.executors.CountList(ctx, p)
}

// ── 写（前端 CRUD 走这里，保证跨进程一致）─────────────────────────────
//
// 每个写方法：写 DB → cachestore.Invalidate（本进程即时清 L1+L2 + 广播失效键给其它进程）。
// 失效的键由写方直接列出（与 ReadThrough 的 fillKeys 对应），无 per-resource 语义 switch。

// scenarioKeys 是一条场景写/删要清的全部缓存键：code + id 双映射（同一份值）
// + 两个全量列表键（enabled 变动跨 true/false 两表，一律清最省心且正确）。
func scenarioKeys(id, code string) []string {
	return []string{
		keyScenarioCode(code), keyScenarioID(id),
		keyScenariosList(true), keyScenariosList(false),
	}
}

// agentKeys 是一次操作员写/删要清的全部缓存键：其 id 键 + complexity 键（code 路，热路径路由用）
// + 两个哨兵键（提/降 planner 或 enabled/kind 变动影响领域池）+ 两个全量列表键。
// complexity 键随此一并失效——两个写入口（SaveExecutor/UpdateExecutorComplexity）都经此，保证移档即时生效。
func agentKeys(id, code string) []string {
	return []string{
		keyExecutorID(id), keyExecutorComplexity(code),
		keyplanner, keyExecutor,
		keyAgentsList(true), keyAgentsList(false),
	}
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

// UpdateExecutor 更新Agent配置（只能更新SystemPrompt、Skills和工具）。
func (s *Store) UpdateExecutor(ctx context.Context, code string, p cfgagent.UpdateParams) (cfgagent.Agent, error) {
	h, err := s.executors.Update(ctx, code, p)
	if err != nil {
		return cfgagent.Agent{}, err
	}
	if err := s.cache.Invalidate(ctx, agentKeys(h.ID, h.Code)...); err != nil {
		return h, err
	}
	return h, nil
}

// UpdateExecutorComplexity 只改单个 agent 的复杂度档位（分档页移档用），失效其缓存键——
// runner 被动清 L1，下次 For(role) 经 ComplexityByCode 读到新档。不碰 agent 其余字段。
func (s *Store) UpdateExecutorComplexity(ctx context.Context, id, complexity string) (cfgagent.Agent, error) {
	h, err := s.executors.UpdateComplexity(ctx, id, complexity)
	if err != nil {
		return cfgagent.Agent{}, err
	}
	if err := s.cache.Invalidate(ctx, agentKeys(h.ID, h.Code)...); err != nil {
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

