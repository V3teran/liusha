// Package configstore 编排 agent 与技能配置的读写：出厂值（internal/builtin，编译进程序）
// 叠加用户层（数据库），经三级缓存（进程内存 L1 → redis L2 → 数据库）供 api 与 runner 读取。
//
// 只缓存用户层（体量小、键空间固定）：agent 用户层、内置技能用户层、自建技能各一个键。
// 出厂值本就在进程内存里，生效值在读取时合成。写路径写库后经 cachestore 广播失效，
// 其它进程被动清本地缓存，下次读即最新。
package configstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/V3teran/liusha/internal/agent"
	"github.com/V3teran/liusha/internal/builtin"
	"github.com/V3teran/liusha/internal/cachestore"
	"github.com/V3teran/liusha/internal/override"
	"github.com/V3teran/liusha/internal/registry"
	"github.com/V3teran/liusha/internal/skillstore"
	"github.com/V3teran/liusha/internal/tools/manifest"
)

// ErrNotFound 标记 agent 或技能不存在（包装 pgx.ErrNoRows，接口层统一映射为 404）。
var ErrNotFound = fmt.Errorf("不存在: %w", pgx.ErrNoRows)

// ErrBuiltinSkill 标记对内置技能做了只适用于自建技能的操作（删除），接口层映射为 409。
var ErrBuiltinSkill = errors.New("内置技能不可删除，可停用或自定义内容")

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

// Store 是 agent 与技能配置的读写入口。
type Store struct {
	agents *agent.Store
	skills *skillstore.Store
	cache  *cachestore.Cache
	tools  ToolCatalog
}

// New 构造 Store。cache 由进程唯一构造并已在后台 Subscribe。
func New(pool *pgxpool.Pool, cache *cachestore.Cache, tools ToolCatalog) *Store {
	return &Store{agents: agent.NewStore(pool), skills: skillstore.New(pool), cache: cache, tools: tools}
}

// ── 缓存（只缓存用户层）─────────────────────────────────────────────

const (
	keyAgentOverrides = "configstore:agent_overrides"
	keySkillOverrides = "configstore:skill_overrides"
	keyCustomSkills   = "configstore:custom_skills"
)

func (s *Store) agentOverrides(ctx context.Context) (map[string]override.Overrides, error) {
	return cachestore.ReadThrough(ctx, s.cache, keyAgentOverrides,
		func(map[string]override.Overrides) []string { return []string{keyAgentOverrides} },
		s.agents.Overrides)
}

func (s *Store) skillOverrides(ctx context.Context) (map[string]override.Overrides, error) {
	return cachestore.ReadThrough(ctx, s.cache, keySkillOverrides,
		func(map[string]override.Overrides) []string { return []string{keySkillOverrides} },
		s.skills.Overrides)
}

func (s *Store) customSkills(ctx context.Context) ([]skillstore.Skill, error) {
	return cachestore.ReadThrough(ctx, s.cache, keyCustomSkills,
		func([]skillstore.Skill) []string { return []string{keyCustomSkills} },
		s.skills.ListCustom)
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

// SkillDetail 是技能页需要的全部信息。内置技能附出厂值与已自定义字段状态。
type SkillDetail struct {
	Skill     skillstore.Skill          `json:"skill"`
	Defaults  *skillstore.Content       `json:"defaults"`
	Overrides map[string]override.State `json:"overrides"`
	// Shadows 为真表示这个自建技能与某内置技能同名，内置版本被遮蔽。
	Shadows bool `json:"shadows_builtin"`
}

// Skills 返回全部生效技能（内置技能叠加用户层 + 自建技能；同名时自建优先），
// 按 category、code 升序。
func (s *Store) Skills(ctx context.Context) ([]skillstore.Skill, error) {
	customs, err := s.customSkills(ctx)
	if err != nil {
		return nil, err
	}
	ovs, err := s.skillOverrides(ctx)
	if err != nil {
		return nil, err
	}
	out := slices.Clone(customs)
	for _, b := range builtin.Skills() {
		if hasCode(customs, b.Code) {
			continue // 被同名自建技能遮蔽
		}
		sk, err := skillstore.Resolve(b, ovs[b.Code])
		if err != nil {
			return nil, err
		}
		out = append(out, sk)
	}
	slices.SortFunc(out, func(a, b skillstore.Skill) int {
		if c := strings.Compare(a.Category, b.Category); c != 0 {
			return c
		}
		return strings.Compare(a.Code, b.Code)
	})
	return out, nil
}

// EnabledSkills 返回全部启用的生效技能（runner 构建技能索引的数据源）。
func (s *Store) EnabledSkills(ctx context.Context) ([]skillstore.Skill, error) {
	all, err := s.Skills(ctx)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(all, func(sk skillstore.Skill) bool { return !sk.Enabled }), nil
}

// Skill 按 code 返回生效技能（read_skill 的后端）。
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

// SkillDetails 返回全部生效技能及其详情（顺序同 Skills）：内置技能附出厂内容与
// 已自定义字段状态，自建技能标明是否遮蔽了同名内置技能。
func (s *Store) SkillDetails(ctx context.Context) ([]SkillDetail, error) {
	skills, err := s.Skills(ctx)
	if err != nil {
		return nil, err
	}
	ovs, err := s.skillOverrides(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]SkillDetail, 0, len(skills))
	for _, sk := range skills {
		d := SkillDetail{Skill: sk, Overrides: map[string]override.State{}}
		b, isBuiltinCode := builtin.SkillByCode(sk.Code)
		if sk.IsBuiltin {
			def := skillstore.Defaults(b)
			d.Defaults = &def
			d.Overrides = override.Describe(skillstore.Schema(), skillstore.DefaultsDocument(b), ovs[sk.Code])
		} else {
			d.Shadows = isBuiltinCode
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

// CreateSkill 新建自建技能。与内置技能或已有自建技能同名返回 skillstore.ErrConflict。
func (s *Store) CreateSkill(ctx context.Context, sk skillstore.Skill) (skillstore.Skill, error) {
	sk.IsBuiltin = false
	if err := skillstore.ValidateNew(sk); err != nil {
		return skillstore.Skill{}, err
	}
	if _, ok := builtin.SkillByCode(sk.Code); ok {
		return skillstore.Skill{}, fmt.Errorf("%w：%s 是内置技能", skillstore.ErrConflict, sk.Code)
	}
	created, err := s.skills.CreateCustom(ctx, sk)
	if err != nil {
		return skillstore.Skill{}, err
	}
	return created, s.cache.Invalidate(ctx, keyCustomSkills)
}

// PatchSkill 修改技能：内置技能写入用户层（与出厂相同即不算自定义），自建技能直接修改。
func (s *Store) PatchSkill(ctx context.Context, code string, changes override.Document) (skillstore.Skill, error) {
	sk, err := s.Skill(ctx, code)
	if err != nil {
		return skillstore.Skill{}, err
	}
	if !sk.IsBuiltin {
		updated, err := s.skills.UpdateCustom(ctx, code, func(cur skillstore.Skill) (skillstore.Content, error) {
			return mergeContent(skillstore.ContentOf(cur), changes)
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return skillstore.Skill{}, fmt.Errorf("技能 %q %w", code, ErrNotFound)
			}
			return skillstore.Skill{}, err
		}
		return updated, s.cache.Invalidate(ctx, keyCustomSkills)
	}
	return s.updateBuiltinSkill(ctx, code, func(def override.Document, cur override.Overrides) (override.Overrides, error) {
		return override.Patch(skillstore.Schema(), def, cur, changes)
	})
}

// ResetSkill 把内置技能的指定字段恢复为出厂内容（不传字段则全部恢复）。
func (s *Store) ResetSkill(ctx context.Context, code string, fields ...string) (skillstore.Skill, error) {
	sk, err := s.Skill(ctx, code)
	if err != nil {
		return skillstore.Skill{}, err
	}
	if !sk.IsBuiltin {
		return skillstore.Skill{}, ErrCustomSkill
	}
	return s.updateBuiltinSkill(ctx, code, func(_ override.Document, cur override.Overrides) (override.Overrides, error) {
		for _, f := range fields {
			if _, ok := skillstore.Schema()[f]; !ok {
				return nil, fmt.Errorf("%w：字段 %q 不可自定义", override.ErrInvalid, f)
			}
		}
		return override.Reset(cur, fields...), nil
	})
}

// DeleteSkill 删除自建技能，并从各 agent 的技能自定义里抹去对它的引用。
func (s *Store) DeleteSkill(ctx context.Context, code string) error {
	sk, err := s.Skill(ctx, code)
	if err != nil {
		return err
	}
	if sk.IsBuiltin {
		return ErrBuiltinSkill
	}
	// 删除与清理各 agent 的引用在一个事务内落定：任一步失败整体回滚、可原样重试，
	// 不会留下「技能已删、引用残留」（残留绑定会在日后同名重建时自动生效）。
	tx, err := s.agents.Begin(ctx)
	if err != nil {
		return fmt.Errorf("删除技能 %s: %w", code, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := s.skills.DeleteCustom(ctx, tx, code); err != nil {
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
	return s.cache.Invalidate(ctx, keyCustomSkills, keyAgentOverrides)
}

// AdoptRetiredSkills 把已下线、但用户自定义过的内置技能转为同 code 的自建技能
// （api 启动时执行，幂等）。返回转换的 code。
func (s *Store) AdoptRetiredSkills(ctx context.Context) ([]string, error) {
	var codes []string
	for _, b := range builtin.Skills() {
		codes = append(codes, b.Code)
	}
	adopted, err := s.skills.AdoptRetired(ctx, codes)
	if err != nil || len(adopted) == 0 {
		return adopted, err
	}
	return adopted, s.cache.Invalidate(ctx, keySkillOverrides, keyCustomSkills)
}

func (s *Store) updateBuiltinSkill(ctx context.Context, code string, edit func(override.Document, override.Overrides) (override.Overrides, error)) (skillstore.Skill, error) {
	b, _ := builtin.SkillByCode(code)
	def := skillstore.DefaultsDocument(b)
	var result skillstore.Skill
	if _, err := s.skills.UpdateOverrides(ctx, code, func(cur override.Overrides) (override.Overrides, skillstore.Skill, error) {
		next, err := edit(def, cur)
		if err != nil {
			return nil, skillstore.Skill{}, err
		}
		if result, err = skillstore.Resolve(b, next); err != nil { // 同 updateAgent：按提交不合法收敛
			return nil, skillstore.Skill{}, fmt.Errorf("%w：%w", override.ErrInvalid, err)
		}
		return next, result, skillstore.Validate(skillstore.ContentOf(result))
	}); err != nil {
		return skillstore.Skill{}, err
	}
	return result, s.cache.Invalidate(ctx, keySkillOverrides)
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

// mergeContent 把提交的字段合入自建技能内容（只接受可修改字段）并校验。
func mergeContent(cur skillstore.Content, changes override.Document) (skillstore.Content, error) {
	schema := skillstore.Schema()
	doc, err := override.ToDocument(cur)
	if err != nil {
		return skillstore.Content{}, err
	}
	for name, v := range changes {
		if _, ok := schema[name]; !ok {
			return skillstore.Content{}, fmt.Errorf("%w：字段 %q 不可修改", override.ErrInvalid, name)
		}
		doc[name] = v
	}
	var next skillstore.Content
	if err := override.FromDocument(doc, &next); err != nil {
		return skillstore.Content{}, fmt.Errorf("%w：%w", override.ErrInvalid, err)
	}
	return next, skillstore.Validate(next)
}

func hasCode(list []skillstore.Skill, code string) bool {
	return slices.ContainsFunc(list, func(sk skillstore.Skill) bool { return sk.Code == code })
}

func mustJSON(s string) json.RawMessage {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return b
}
