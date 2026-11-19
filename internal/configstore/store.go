// Package configstore 编排 agent、技能与场景配置的读写，经三级缓存（进程内存 L1 → redis L2
// → 数据库）供 api 与 runner 读取。
//
//   - agent：出厂值（internal/builtin，编译进程序）叠加用户层（agent_override），只缓存用户层，
//     生效值在读取时合成；
//   - 技能、场景：一张表一行一个资源（内置的出厂内容是种子，迁移后由出厂同步写入），缓存整表。
//
// 写路径写库后经 cachestore 广播失效，其它进程被动清本地缓存，下次读即最新。
package configstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/V3teran/liusha/internal/agent"
	"github.com/V3teran/liusha/internal/builtin"
	"github.com/V3teran/liusha/internal/builtinsync"
	"github.com/V3teran/liusha/internal/cachestore"
	"github.com/V3teran/liusha/internal/override"
	"github.com/V3teran/liusha/internal/registry"
	"github.com/V3teran/liusha/internal/scenariostore"
	"github.com/V3teran/liusha/internal/skillstore"
	"github.com/V3teran/liusha/internal/tools/manifest"
)

// ErrNotFound 标记 agent 或技能不存在（包装 pgx.ErrNoRows，接口层统一映射为 404）。
var ErrNotFound = fmt.Errorf("不存在: %w", pgx.ErrNoRows)

// ErrCustomSkill 标记对自建技能做了只适用于内置技能的操作（恢复出厂），接口层映射为 409。
var ErrCustomSkill = errors.New("自建技能没有出厂内容可恢复")

// ToolCatalog 是工具全集：展开 agent 工具白名单的「全部」、校验工具引用。
type ToolCatalog struct {
	FunctionTools []string // 内置函数工具（registry.FunctionToolCatalog）
	CliTools      []string // 沙箱 CLI 工具（工具清单）
}

// ToolCatalogFrom 由函数工具目录与 CLI 工具清单组装工具全集；m 为 nil 时不含 CLI 工具（测试只关心函数工具时用）。
func ToolCatalogFrom(m *manifest.ToolManifest) ToolCatalog {
	var tc ToolCatalog
	for _, meta := range registry.FunctionToolCatalog {
		tc.FunctionTools = append(tc.FunctionTools, meta.Name)
	}
	if m != nil {
		for _, t := range m.Tools {
			tc.CliTools = append(tc.CliTools, t.Name)
		}
	}
	return tc
}

// Store 是 agent、技能与场景配置的读写入口。
type Store struct {
	agents    *agent.Store
	skills    *skillstore.Store
	scenarios *scenariostore.Store
	cache     *cachestore.Cache
	tools     ToolCatalog
}

// New 构造 Store。cache 由进程唯一构造并已在后台 Subscribe。
func New(pool *pgxpool.Pool, cache *cachestore.Cache, tools ToolCatalog) *Store {
	return &Store{
		agents: agent.NewStore(pool), skills: skillstore.New(pool), scenarios: scenariostore.New(pool),
		cache: cache, tools: tools,
	}
}

// ── 缓存（只缓存用户层）─────────────────────────────────────────────

const keyAgentOverrides = "configstore:agent_overrides"

// 技能表的缓存键带出厂内容摘要：出厂同步改写了表的新版本进程，不会读到旧版本留在 L2 的副本。
var keySkills = "configstore:skills:" + builtinsync.Digest(skillstore.Seeds())

func (s *Store) agentOverrides(ctx context.Context) (map[string]override.Overrides, error) {
	return cachestore.ReadThrough(ctx, s.cache, keyAgentOverrides,
		func(map[string]override.Overrides) []string { return []string{keyAgentOverrides} },
		s.agents.Overrides)
}

func (s *Store) skillRecords(ctx context.Context) ([]skillstore.Record, error) {
	return cachestore.ReadThrough(ctx, s.cache, keySkills,
		func([]skillstore.Record) []string { return []string{keySkills} },
		s.skills.List)
}

// ── agent ───────────────────────────────────────────────────────────

// AgentDetail 是 agent 配置页需要的全部信息。
type AgentDetail struct {
	Agent         agent.Agent               `json:"agent"`
	Defaults      agent.Config              `json:"defaults"`
	Overrides     map[string]override.State `json:"overrides"`
	PromptArchive *agent.PromptArchive      `json:"prompt_archive"`
}

// Agent 返回一个 agent 的生效配置（runner 每个任务经此装配四个 agent，热路径）。
func (s *Store) Agent(ctx context.Context, code string) (agent.Agent, error) {
	b, ok := builtin.AgentByCode(code)
	if !ok {
		return agent.Agent{}, fmt.Errorf("agent %q %w", code, ErrNotFound)
	}
	ovs, err := s.agentOverrides(ctx)
	if err != nil {
		return agent.Agent{}, err
	}
	catalog, err := s.agentCatalog(ctx)
	if err != nil {
		return agent.Agent{}, err
	}
	return agent.Resolve(b, ovs[code], catalog)
}

// Agents 返回全部 agent 的生效配置（按 code 升序）。
func (s *Store) Agents(ctx context.Context) ([]agent.Agent, error) {
	ovs, err := s.agentOverrides(ctx)
	if err != nil {
		return nil, err
	}
	catalog, err := s.agentCatalog(ctx)
	if err != nil {
		return nil, err
	}
	var out []agent.Agent
	for _, b := range builtin.Agents() {
		p, err := agent.Resolve(b, ovs[b.Code], catalog)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// AgentDetails 返回全部 agent 的生效配置、出厂值、各已自定义字段状态与提示词归档
// （按 code 升序）。
func (s *Store) AgentDetails(ctx context.Context) ([]AgentDetail, error) {
	ovs, err := s.agentOverrides(ctx)
	if err != nil {
		return nil, err
	}
	catalog, err := s.agentCatalog(ctx)
	if err != nil {
		return nil, err
	}
	archives, err := s.agents.PromptArchives(ctx)
	if err != nil {
		return nil, err
	}
	schema := agent.Schema(catalog)
	var out []AgentDetail
	for _, b := range builtin.Agents() {
		p, err := agent.Resolve(b, ovs[b.Code], catalog)
		if err != nil {
			return nil, err
		}
		d := AgentDetail{
			Agent:     p,
			Defaults:  agent.Defaults(b),
			Overrides: override.Describe(schema, agent.DefaultsDocument(b), ovs[b.Code]),
		}
		if a, ok := archives[b.Code]; ok {
			d.PromptArchive = &a
		}
		out = append(out, d)
	}
	return out, nil
}

// AgentDetail 返回单个 agent 的详情（见 AgentDetails）。
func (s *Store) AgentDetail(ctx context.Context, code string) (AgentDetail, error) {
	all, err := s.AgentDetails(ctx)
	if err != nil {
		return AgentDetail{}, err
	}
	for _, d := range all {
		if d.Agent.Code == code {
			return d, nil
		}
	}
	return AgentDetail{}, fmt.Errorf("agent %q %w", code, ErrNotFound)
}

// PatchAgent 写入用户提交的字段（Replace 字段为新值，集合字段为 {on, off}），
// 校验应用后的生效配置，返回新的生效配置。
func (s *Store) PatchAgent(ctx context.Context, code string, changes override.Document) (agent.Agent, error) {
	return s.updateAgent(ctx, code, func(schema override.Schema, def override.Document, cur override.Overrides) (override.Overrides, error) {
		return override.Patch(schema, def, cur, changes)
	})
}

// ToggleAgent 对 agent 的集合字段（function_tools / cli_tools / skills）勾选或取消单个元素。
func (s *Store) ToggleAgent(ctx context.Context, code, field, elem string, on bool) (agent.Agent, error) {
	return s.updateAgent(ctx, code, func(schema override.Schema, def override.Document, cur override.Overrides) (override.Overrides, error) {
		return override.Toggle(schema, def, cur, field, mustJSON(elem), on)
	})
}

// ResetAgent 把指定字段恢复为出厂值（不传字段则全部恢复）。
func (s *Store) ResetAgent(ctx context.Context, code string, fields ...string) (agent.Agent, error) {
	return s.updateAgent(ctx, code, func(schema override.Schema, _ override.Document, cur override.Overrides) (override.Overrides, error) {
		for _, f := range fields {
			if _, ok := schema[f]; !ok {
				return nil, fmt.Errorf("%w：字段 %q 不可自定义", override.ErrInvalid, f)
			}
		}
		return override.Reset(cur, fields...), nil
	})
}

// DeletePromptArchive 删除 agent 的提示词归档（用户已对照处理）。
func (s *Store) DeletePromptArchive(ctx context.Context, code string) error {
	if _, ok := builtin.AgentByCode(code); !ok {
		return fmt.Errorf("agent %q %w", code, ErrNotFound)
	}
	return s.agents.DeletePromptArchive(ctx, code)
}

type agentEdit func(schema override.Schema, defaults override.Document, cur override.Overrides) (override.Overrides, error)

func (s *Store) updateAgent(ctx context.Context, code string, edit agentEdit) (agent.Agent, error) {
	b, ok := builtin.AgentByCode(code)
	if !ok {
		return agent.Agent{}, fmt.Errorf("agent %q %w", code, ErrNotFound)
	}
	catalog, err := s.agentCatalog(ctx)
	if err != nil {
		return agent.Agent{}, err
	}
	schema, def := agent.Schema(catalog), agent.DefaultsDocument(b)
	var result agent.Agent
	if _, err := s.agents.UpdateOverrides(ctx, code, func(cur override.Overrides) (override.Overrides, error) {
		next, err := edit(schema, def, cur)
		if err != nil {
			return nil, err
		}
		// 写入路径：合成失败只可能来自本次提交（已存用户层合成成功才落过库），按配置
		// 不合法收敛为 400，而不是当作服务端故障。
		if result, err = agent.Resolve(b, next, catalog); err != nil {
			return nil, fmt.Errorf("%w：%w", override.ErrInvalid, err)
		}
		return next, agent.Validate(result.Config)
	}); err != nil {
		return agent.Agent{}, err
	}
	return result, s.cache.Invalidate(ctx, keyAgentOverrides)
}

// agentCatalog 组装集合字段的全集；技能全集含内置与自建技能。
func (s *Store) agentCatalog(ctx context.Context) (agent.Catalog, error) {
	skills, err := s.Skills(ctx)
	if err != nil {
		return agent.Catalog{}, err
	}
	codes := make([]string, 0, len(skills))
	for _, sk := range skills {
		codes = append(codes, sk.Code)
	}
	return agent.Catalog{
		FunctionTools: func() []string { return s.tools.FunctionTools },
		CliTools:      func() []string { return s.tools.CliTools },
		Skills:        func() []string { return codes },
	}, nil
}

// ── 技能 ────────────────────────────────────────────────────────────

// SkillDetail 是技能页需要的全部信息。内置技能附出厂内容与各字段相对出厂的状态。
type SkillDetail struct {
	Skill     skillstore.Skill          `json:"skill"`
	Defaults  *skillstore.Content       `json:"defaults"`
	Overrides map[string]override.State `json:"overrides"`
	// Shadows 为真表示这个自建技能与某内置技能同名，内置版本被遮蔽。
	Shadows bool `json:"shadows_builtin"`
}

// liveSkills 返回未被删除的技能行（按 category、code 升序）。
func (s *Store) liveSkills(ctx context.Context) ([]skillstore.Record, error) {
	recs, err := s.skillRecords(ctx)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(slices.Clone(recs), func(r skillstore.Record) bool { return r.Deleted }), nil
}

// Skills 返回全部技能（不含已删除的内置技能，按 category、code 升序）。
func (s *Store) Skills(ctx context.Context) ([]skillstore.Skill, error) {
	recs, err := s.liveSkills(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]skillstore.Skill, 0, len(recs))
	for _, r := range recs {
		out = append(out, r.Skill)
	}
	return out, nil
}

// DeletedSkills 返回被用户删除、可恢复的内置技能（名称与描述取当前出厂内容，恢复即得到它）。
func (s *Store) DeletedSkills(ctx context.Context) ([]skillstore.Skill, error) {
	recs, err := s.skillRecords(ctx)
	if err != nil {
		return nil, err
	}
	var out []skillstore.Skill
	for _, r := range recs {
		if b, ok := builtin.SkillByCode(r.Code); ok && r.Deleted {
			out = append(out, skillstore.Skill{
				Code: b.Code, Category: b.Category, Name: b.Name, Description: b.Description, Body: b.Body,
				Enabled: true, IsBuiltin: true,
			})
		}
	}
	return out, nil
}

// RestoreSkill 把用户删除的内置技能恢复成当前出厂内容。不是已删除的内置技能返回 ErrNotFound。
func (s *Store) RestoreSkill(ctx context.Context, code string) (skillstore.Skill, error) {
	if err := s.skills.Restore(ctx, code); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return skillstore.Skill{}, fmt.Errorf("已删除的内置技能 %q %w", code, ErrNotFound)
		}
		return skillstore.Skill{}, err
	}
	if err := s.cache.Invalidate(ctx, keySkills); err != nil {
		return skillstore.Skill{}, err
	}
	return s.Skill(ctx, code)
}

// EnabledSkills 返回全部启用的技能（runner 构建技能索引的数据源）。
func (s *Store) EnabledSkills(ctx context.Context) ([]skillstore.Skill, error) {
	all, err := s.Skills(ctx)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(all, func(sk skillstore.Skill) bool { return !sk.Enabled }), nil
}

// Skill 按 code 返回技能（read_skill 的后端）。
func (s *Store) Skill(ctx context.Context, code string) (skillstore.Skill, error) {
	all, err := s.Skills(ctx)
	if err != nil {
		return skillstore.Skill{}, err
	}
	for _, sk := range all {
		if sk.Code == code {
			return sk, nil
		}
	}
	return skillstore.Skill{}, fmt.Errorf("技能 %q %w", code, ErrNotFound)
}

// SkillDetails 返回全部技能及其详情（顺序同 Skills）：内置技能附出厂内容与各字段相对出厂的
// 状态，自建技能标明是否遮蔽了同名内置技能。
func (s *Store) SkillDetails(ctx context.Context) ([]SkillDetail, error) {
	recs, err := s.liveSkills(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]SkillDetail, 0, len(recs))
	for _, r := range recs {
		d := SkillDetail{Skill: r.Skill, Overrides: map[string]override.State{}}
		b, seeded := builtin.SkillByCode(r.Code)
		switch {
		case r.IsBuiltin && seeded:
			def := skillstore.Defaults(b)
			d.Defaults = &def
			ov, err := describeAgainstSeed(skillstore.ContentOf(r.Skill), def, r.SeedHash, skillstore.SeedHash(b))
			if err != nil {
				return nil, fmt.Errorf("技能 %s: %w", r.Code, err)
			}
			d.Overrides = ov
		case !r.IsBuiltin:
			d.Shadows = seeded
		}
		out = append(out, d)
	}
	return out, nil
}

// SkillDetail 返回单个技能的详情（见 SkillDetails）。
func (s *Store) SkillDetail(ctx context.Context, code string) (SkillDetail, error) {
	all, err := s.SkillDetails(ctx)
	if err != nil {
		return SkillDetail{}, err
	}
	for _, d := range all {
		if d.Skill.Code == code {
			return d, nil
		}
	}
	return SkillDetail{}, fmt.Errorf("技能 %q %w", code, ErrNotFound)
}

// CreateSkill 新建自建技能。与内置技能或已有技能同名返回 skillstore.ErrConflict。
func (s *Store) CreateSkill(ctx context.Context, sk skillstore.Skill) (skillstore.Skill, error) {
	sk.IsBuiltin = false
	if err := skillstore.ValidateNew(sk); err != nil {
		return skillstore.Skill{}, err
	}
	if _, ok := builtin.SkillByCode(sk.Code); ok {
		return skillstore.Skill{}, fmt.Errorf("%w：%s 是内置技能", skillstore.ErrConflict, sk.Code)
	}
	created, err := s.skills.Create(ctx, sk)
	if err != nil {
		return skillstore.Skill{}, err
	}
	return created.Skill, s.cache.Invalidate(ctx, keySkills)
}

// PatchSkill 修改技能的可修改字段（内置与自建同一路径）。
func (s *Store) PatchSkill(ctx context.Context, code string, changes override.Document) (skillstore.Skill, error) {
	return s.updateSkill(ctx, code, func(r skillstore.Record) (skillstore.Content, error) {
		return mergeContent(skillstore.ContentOf(r.Skill), changes)
	})
}

// ResetSkill 把内置技能的指定字段恢复为出厂内容（不传字段则全部恢复）。
func (s *Store) ResetSkill(ctx context.Context, code string, fields ...string) (skillstore.Skill, error) {
	return s.updateSkill(ctx, code, func(r skillstore.Record) (skillstore.Content, error) {
		b, ok := builtin.SkillByCode(code)
		if !r.IsBuiltin || !ok {
			return skillstore.Content{}, ErrCustomSkill
		}
		return resetContent(skillstore.ContentOf(r.Skill), skillstore.Defaults(b), fields)
	})
}

func (s *Store) updateSkill(ctx context.Context, code string, fn func(skillstore.Record) (skillstore.Content, error)) (skillstore.Skill, error) {
	updated, err := s.skills.Update(ctx, code, fn)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return skillstore.Skill{}, fmt.Errorf("技能 %q %w", code, ErrNotFound)
		}
		return skillstore.Skill{}, err
	}
	return updated.Skill, s.cache.Invalidate(ctx, keySkills)
}

// DeleteSkill 删除技能（自建删行，内置记删除标记、可恢复），并从各 agent 的技能自定义里抹去
// 对它的引用。
func (s *Store) DeleteSkill(ctx context.Context, code string) error {
	// 删除与清理各 agent 的引用在一个事务内落定：任一步失败整体回滚、可原样重试，
	// 不会留下「技能已删、引用残留」（残留绑定会在日后同名重建时自动生效）。
	tx, err := s.agents.Begin(ctx)
	if err != nil {
		return fmt.Errorf("删除技能 %s: %w", code, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := s.skills.Delete(ctx, tx, code); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("技能 %q %w", code, ErrNotFound)
		}
		return err
	}
	if err := s.removeSkillReferences(ctx, tx, code); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("删除技能 %s: %w", code, err)
	}
	return s.cache.Invalidate(ctx, keySkills, keyAgentOverrides)
}

// removeSkillReferences 在调用方事务内从各 agent 技能字段的新增、删除记录里抹去被删技能。
// 按 agent code 固定顺序逐个取资源级锁，在锁内重读用户层（不依赖事务外的快照）。
func (s *Store) removeSkillReferences(ctx context.Context, tx pgx.Tx, code string) error {
	elem := mustJSON(code)
	for _, b := range builtin.Agents() {
		if _, err := s.agents.UpdateOverridesTx(ctx, tx, b.Code, func(cur override.Overrides) (override.Overrides, error) {
			next, _ := override.RemoveMember(cur, agent.FieldSkills, elem)
			return next, nil
		}); err != nil {
			return fmt.Errorf("清理 %s 对技能 %s 的引用: %w", b.Code, code, err)
		}
	}
	return nil
}

func mustJSON(s string) json.RawMessage {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return b
}
