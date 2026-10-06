// Package cache 是配置的多级缓存读写层，构建在资源无关的
// cachestore 内核之上：内存 L1（本进程）→ redis L2（跨进程共享 + 失效总线）→ DB（事实源）。
//
// 为何分层（见 D7）：api 与 runner 是**多进程**。前端在 api 改配置后，runner 的本地
// L1 必须被动失效，否则 runner 用旧配置装配。故写路径写 DB 后经 cachestore 广播失效键，
// 各进程共享的 cachestore.Subscribe goroutine 收到即清本地 L1 + L2，下次读回填最新值。
//
// 本包只贡献 agent 专有的缓存键与读写方法，缓存机制（L1/L2/总线）全在 cachestore。
//
// 缓存粒度：单条读 + **key 空间固定**的读均走 L1/L2 缓存——单条读（id/code）、
// 全量列表读（ListExecutors 按 onlyEnabled 分 2 键）、两个哨兵（planner/
// enabled_domain）。任一成员写即失效对应固定键。唯**分页/搜索列表**（ListExecutorsPaged
// + Count）不缓存：其键含搜索词 × limit × offset，key 空间随查询无限
// 增长，L1 无 TTL 会堆积孤儿键（内存泄漏），且配置管理页低频，缓存收益近零，故直穿 DB。
package cache

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/V3teran/liusha/internal/agent"
	"github.com/V3teran/liusha/internal/cachestore"
	"github.com/V3teran/liusha/internal/skillstore"
)

// executorStore 是 configstore 依赖的 agent 底层能力（*agent.Store 满足）。
type executorStore interface {
	GetByID(ctx context.Context, id string) (agent.Agent, error)
	GetByCode(ctx context.Context, code string) (agent.Agent, error)
	Update(ctx context.Context, code string, p agent.UpdateParams) (agent.Agent, error)
	UpdateComplexity(ctx context.Context, id, complexity string) (agent.Agent, error)
	List(ctx context.Context, onlyEnabled bool) ([]agent.Agent, error)
	ListPaged(ctx context.Context, p agent.ListParams) ([]agent.Agent, error)
	CountList(ctx context.Context, p agent.ListParams) (int, error)
}

// skillStore 是 configstore 依赖的 skill 底层能力（*skillstore.Store 满足）。
type skillStore interface {
	GetByID(ctx context.Context, id string) (skillstore.Skill, error)
	GetByCode(ctx context.Context, code string) (skillstore.Skill, error)
	List(ctx context.Context, p skillstore.ListParams) ([]skillstore.Skill, error)
	ListEnabled(ctx context.Context) ([]skillstore.Skill, error)
	Create(ctx context.Context, sk skillstore.Skill) (skillstore.Skill, error)
	Update(ctx context.Context, id string, p skillstore.UpdateParams) (skillstore.Skill, error)
	Delete(ctx context.Context, id string) error
}

// Store 编排 agent 和 skill 的多级读写：底层 DB store + 共享 cachestore 内核。
type Store struct {
	executors executorStore
	skills    skillStore
	cache     *cachestore.Cache
}

// New 用 pgxpool + 共享 cachestore 构造 Store（生产装配用）。
// cache 由进程唯一构造并已 go cache.Subscribe(ctx)，可被多个资源仓储共享。
func New(pool *pgxpool.Pool, cache *cachestore.Cache) *Store {
	return newWithStores(
		agent.NewStore(pool),
		skillstore.NewStore(pool),
		cache,
	)
}

// newWithStores 用已构造的底层 store 装配（测试注入 mock 用）。
func newWithStores(hn executorStore, sk skillStore, cache *cachestore.Cache) *Store {
	return &Store{executors: hn, skills: sk, cache: cache}
}

// ── 缓存键（L1/L2 同键，统一前缀 configstore:）───────────────────────────

func keyExecutorID(id string) string     { return "configstore:executor:id:" + id }
func keyExecutorCode(code string) string { return "configstore:executor:code:" + code }

// keyAgentsList 是全量列表读的缓存键，按 onlyEnabled 分两键（有界）。
// 任一 agent 写即失效其资源的两个 list 键（enabled 变动会跨 true/false 两表）。
func keyAgentsList(onlyEnabled bool) string {
	return "configstore:executors:list:" + boolKey(onlyEnabled)
}

// boolKey 把 onlyEnabled 稳定映射为键后缀。
func boolKey(b bool) string {
	if b {
		return "enabled"
	}
	return "all"
}

// ── 小工具 ────────────────────────────────────────────────────────────

// ── 单条读（L1/L2 缓存）───────────────────────────────────────────────

// ExecutorByID 按 uuid 读操作员（CRUD :id）。
func (s *Store) ExecutorByID(ctx context.Context, id string) (agent.Agent, error) {
	return cachestore.ReadThrough(ctx, s.cache, keyExecutorID(id),
		func(h agent.Agent) []string { return []string{keyExecutorID(h.ID)} },
		func(ctx context.Context) (agent.Agent, error) {
			return s.executors.GetByID(ctx, id)
		})
}

// ExecutorByCode 按 code 读操作员（走 code 键多级缓存，见 AgentByCode）。
func (s *Store) ExecutorByCode(ctx context.Context, code string) (agent.Agent, error) {
	return s.AgentByCode(ctx, code)
}

// AgentByCode 按 code 读任意 agent（planner/executor/evaluator/monitor），走
// keyExecutorCode 的 L1/L2/DB 多级缓存。回填 code+id 双键（载荷同型 agent.Agent；
// complexity 键载荷是 complexityResult，不可混填——见 decode 的类型约束）。
// 一致性：所有 agent 写路径（UpdateExecutor/UpdateAgent/UpdateExecutorComplexity）
// 经 agentKeys 失效 code 键，前端改配置 → 总线广播 → runner 清 L1 → 下次读到新值。
// runner 的认知循环每任务经此读四 agent 配置（热路径，L1 命中为主）。
func (s *Store) AgentByCode(ctx context.Context, code string) (agent.Agent, error) {
	return cachestore.ReadThrough(ctx, s.cache, keyExecutorCode(code),
		func(h agent.Agent) []string {
			return []string{keyExecutorCode(h.Code), keyExecutorID(h.ID)}
		},
		func(ctx context.Context) (agent.Agent, error) {
			return s.executors.GetByCode(ctx, code)
		})
}

// ── 全量列表读（L1/L2 缓存，按 onlyEnabled 分键）─────────────────────────

// ListExecutors 全量列表读，走多级缓存（按 onlyEnabled 分键）。任一操作员写即失效两键。
func (s *Store) ListExecutors(ctx context.Context, onlyEnabled bool) ([]agent.Agent, error) {
	return cachestore.ReadThrough(ctx, s.cache, keyAgentsList(onlyEnabled),
		func([]agent.Agent) []string { return []string{keyAgentsList(onlyEnabled)} },
		func(ctx context.Context) ([]agent.Agent, error) {
			return s.executors.List(ctx, onlyEnabled)
		})
}

// ── 分页/搜索列表读（不缓存，直穿 DB）─────────────────────────────────
//
// 键含搜索词 × limit × offset，key 空间随查询无限增长；L1 无 TTL 会堆积孤儿键（内存泄漏）。
// 配置管理页低频，缓存收益近零，故直穿 DB（与 liusha2 一致）。

// ListExecutorsPaged 直穿底层 store：搜索 + 分页（配置管理页）。
func (s *Store) ListExecutorsPaged(ctx context.Context, p agent.ListParams) ([]agent.Agent, error) {
	return s.executors.ListPaged(ctx, p)
}

// CountExecutors 直穿底层 store：与 ListExecutorsPaged 同过滤的总数。
func (s *Store) CountExecutors(ctx context.Context, p agent.ListParams) (int, error) {
	return s.executors.CountList(ctx, p)
}

// ── 写（前端 CRUD 走这里，保证跨进程一致）─────────────────────────────
//
// 每个写方法：写 DB → cachestore.Invalidate（本进程即时清 L1+L2 + 广播失效键给其它进程）。
// 失效的键由写方直接列出（与 ReadThrough 的 fillKeys 对应），无 per-resource 语义 switch。

// agentKeys 是一次操作员写/删要清的全部缓存键：id 键 + code 键 + 两个全量列表键。失效集是各 ReadThrough 回填键的超集
// （decode 失败即硬错，回填键载荷类型必须一致；失效键不受此限）。
// complexity/code 键随此一并失效——全部写入口都经此，保证移档/改配即时生效。
func agentKeys(id, code string) []string {
	return []string{
		keyExecutorID(id), keyExecutorCode(code),
		keyAgentsList(true), keyAgentsList(false),
	}
}

// UpdateExecutor 更新Agent配置（只能更新SystemPrompt、Skills和工具）。
func (s *Store) UpdateExecutor(ctx context.Context, code string, p agent.UpdateParams) (agent.Agent, error) {
	h, err := s.executors.Update(ctx, code, p)
	if err != nil {
		return agent.Agent{}, err
	}
	if err := s.cache.Invalidate(ctx, agentKeys(h.ID, h.Code)...); err != nil {
		return h, err
	}
	return h, nil
}

// UpdateExecutorComplexity 只改单个 agent 的复杂度档位（分档页移档用），失效其缓存键——
// runner 被动清 L1，下次 For(role) 经 ComplexityByCode 读到新档。不碰 agent 其余字段。
func (s *Store) UpdateExecutorComplexity(ctx context.Context, id, complexity string) (agent.Agent, error) {
	h, err := s.executors.UpdateComplexity(ctx, id, complexity)
	if err != nil {
		return agent.Agent{}, err
	}
	if err := s.cache.Invalidate(ctx, agentKeys(h.ID, h.Code)...); err != nil {
		return h, err
	}
	return h, nil
}

// ── 通用 Agent 方法（支持 Planner/Executor/Evaluator/Monitor）─────────

// UpdateAgent 更新任意 Agent 配置，失效缓存
func (s *Store) UpdateAgent(ctx context.Context, id string, p agent.UpdateParams) (agent.Agent, error) {
	h, err := s.executors.Update(ctx, id, p)
	if err != nil {
		return agent.Agent{}, err
	}
	if err := s.cache.Invalidate(ctx, agentKeys(h.ID, h.Code)...); err != nil {
		return h, err
	}
	return h, nil
}

// ── Skill 缓存方法 ────────────────────────────────────────────────────

func keySkillID(id string) string     { return "configstore:skill:id:" + id }
func keySkillCode(code string) string { return "configstore:skill:code:" + code }

// keySkillsEnabledList 是「全部启用 skill」哨兵键（key 空间有界：仅此一键）。
// runner 的 Tier 1 技能索引按它读；任何 skill 增/改/删都失效它。
const keySkillsEnabledList = "configstore:skills:list:enabled"

// skillFillKeys 是单条 skill 读的回填键（载荷类型 skillstore.Skill，code/id 双键交叉回填）。
func skillFillKeys(id, code string) []string {
	return []string{keySkillID(id), keySkillCode(code)}
}

// skillKeys 是一次 skill 写要失效的键集：回填键超集 + 启用列表哨兵键
// （列表载荷是 []skillstore.Skill，不可被单条读回填——类型不同会 decode 硬错）。
func skillKeys(id, code string) []string {
	return []string{keySkillID(id), keySkillCode(code), keySkillsEnabledList}
}

// SkillByID 按 uuid 读 Skill（L1/L2 缓存）
func (s *Store) SkillByID(ctx context.Context, id string) (skillstore.Skill, error) {
	return cachestore.ReadThrough(ctx, s.cache, keySkillID(id),
		func(sk skillstore.Skill) []string { return skillFillKeys(sk.ID, sk.Code) },
		func(ctx context.Context) (skillstore.Skill, error) {
			return s.skills.GetByID(ctx, id)
		})
}

// SkillByCode 按 code 读 Skill（L1/L2 缓存）——read_skill 工具（Tier 2）的后端。
func (s *Store) SkillByCode(ctx context.Context, code string) (skillstore.Skill, error) {
	return cachestore.ReadThrough(ctx, s.cache, keySkillCode(code),
		func(sk skillstore.Skill) []string { return skillFillKeys(sk.ID, sk.Code) },
		func(ctx context.Context) (skillstore.Skill, error) {
			return s.skills.GetByCode(ctx, code)
		})
}

// EnabledSkills 返回全部启用的 skill（无分页截断——skill 总量本身有界），
// 缓存于哨兵键。runner 每任务构建 Tier 1 技能索引的数据源；前端改 skill
// （正文/启停）经 UpdateSkill/CreateSkill/DeleteSkill 失效此键并广播，runner 下次即新。
func (s *Store) EnabledSkills(ctx context.Context) ([]skillstore.Skill, error) {
	return cachestore.ReadThrough(ctx, s.cache, keySkillsEnabledList,
		func([]skillstore.Skill) []string { return []string{keySkillsEnabledList} },
		func(ctx context.Context) ([]skillstore.Skill, error) {
			return s.skills.ListEnabled(ctx)
		})
}

// ListSkills 列出 Skill（直穿 DB，不缓存）
// 原因：支持搜索和分页，key 空间无限，缓存收益低
func (s *Store) ListSkills(ctx context.Context, p skillstore.ListParams) ([]skillstore.Skill, error) {
	return s.skills.List(ctx, p)
}

// UpdateSkill 更新 Skill，失效缓存（单条键 + 启用列表键）
func (s *Store) UpdateSkill(ctx context.Context, id string, p skillstore.UpdateParams) (skillstore.Skill, error) {
	sk, err := s.skills.Update(ctx, id, p)
	if err != nil {
		return skillstore.Skill{}, err
	}
	if err := s.cache.Invalidate(ctx, skillKeys(sk.ID, sk.Code)...); err != nil {
		return sk, err
	}
	return sk, nil
}

// CreateSkill 创建 Skill（写穿 DB 后失效启用列表键——新 skill 会出现在列表里；
// 其 code/id 键尚无旧缓存，无需失效）
func (s *Store) CreateSkill(ctx context.Context, sk skillstore.Skill) (skillstore.Skill, error) {
	created, err := s.skills.Create(ctx, sk)
	if err != nil {
		return skillstore.Skill{}, err
	}
	_ = s.cache.Invalidate(ctx, keySkillsEnabledList) // best-effort：失败由 10min L2 TTL 兜底
	return created, nil
}

// DeleteSkill 删除 Skill，失效缓存
func (s *Store) DeleteSkill(ctx context.Context, id string) error {
	// 先查询获取 code，用于失效缓存
	sk, err := s.skills.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if err := s.skills.Delete(ctx, id); err != nil {
		return err
	}
	// 删除成功后失效缓存（best-effort，失败不影响删除结果）
	_ = s.cache.Invalidate(ctx, skillKeys(sk.ID, sk.Code)...)
	return nil
}

// InvalidateSkills 供绕过本 Store 的写路径（api 启动期种子 import、reseed 工具）
// 事后统一失效：启用列表键 + 受影响 code 的单条键。ids 无从得知（行已删/未建），
// code 键失效已足够——id 键只服务前端 :id 路由，种子路径不触碰。
func (s *Store) InvalidateSkills(ctx context.Context, codes ...string) error {
	keys := make([]string, 0, len(codes)+1)
	keys = append(keys, keySkillsEnabledList)
	for _, c := range codes {
		if c != "" {
			keys = append(keys, keySkillCode(c))
		}
	}
	return s.cache.Invalidate(ctx, keys...)
}

// InvalidateAgents 供 reseed 强制覆盖 agents 后按 code 失效全部相关键
// （经 DB 回读拿 id，重建 agentKeys 全集）。低频工具路径，直读可接受。
func (s *Store) InvalidateAgents(ctx context.Context, codes ...string) error {
	var keys []string
	for _, code := range codes {
		if code == "" {
			continue
		}
		h, err := s.executors.GetByCode(ctx, code)
		if err != nil {
			continue // 行不存在：无旧缓存可失效，跳过
		}
		keys = append(keys, agentKeys(h.ID, h.Code)...)
	}
	if len(keys) == 0 {
		return nil
	}
	return s.cache.Invalidate(ctx, keys...)
}
