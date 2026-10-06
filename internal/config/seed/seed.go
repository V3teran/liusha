// Package seed 把磁盘上的 agent 配置首次导入 DB。
//
// insert-only 首填语义（见 D6）：DB 是事实源，种子只填**空库**，按 code 判存在——
// 已存在的行一律跳过，绝不覆盖前端/运维在 DB 里的改动。
//
// 目录约定（dir 为配置根）：
//   - dir/agents/*.md   ：操作员 charter（frontmatter 元信息 + body 方法论正文）
package seed

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5"
	"gopkg.in/yaml.v3"

	agent "github.com/V3teran/liusha/internal/agent"
	skill "github.com/V3teran/liusha/internal/skillstore"
)

// agentFront 是 agents/*.md frontmatter 的解析目标。
// id 用作稳定引用键 code（四角色即四个固定 code）；body 取 markdown 正文。
// function_tools 是内置函数工具（进程内原生函数 code 列表）。
// cli_tools 是外置 CLI 工具集（tools.yaml 名字），严格白名单，空=不装配任何外部工具。
// skills 是 Agent 可访问的 skill 裸名列表（= skills/<分类>/<名字> 的目录名，
// 如 ["bac", "browser-use", "dom-xss"]）——渐进式加载的白名单：声明的进
// system prompt 技能索引（Tier 1）并可经 read_skill 拉正文（Tier 2）。
type agentFront struct {
	ID            string   `yaml:"id"`
	Name          string   `yaml:"name"`
	Description   string   `yaml:"description"`
	FunctionTools []string `yaml:"function_tools"`
	CliTools      []string `yaml:"cli_tools"`
	Skills        []string `yaml:"skills"`
	MaxIterations int      `yaml:"max_iterations"`
	Complexity    string   `yaml:"complexity"` // LLM 档位 simple|medium|complex
}

var (
	frontDelim = []byte("---")
	newline    = []byte("\n")
)

// splitFrontmatter 切 markdown 成 (frontmatter, body)。约定同 Jekyll/Hugo：开头 ---\n yaml \n---。
func splitFrontmatter(raw []byte) ([]byte, []byte, error) {
	r := bytes.TrimLeft(raw, "\n\r\t ")
	if !bytes.HasPrefix(r, frontDelim) {
		return nil, nil, errors.New("缺 frontmatter：开头未发现 '---'")
	}
	r = r[len(frontDelim):]
	closeMark := append(append([]byte{}, newline...), frontDelim...)
	idx := bytes.Index(r, closeMark)
	if idx < 0 {
		return nil, nil, errors.New("frontmatter 未闭合：缺 '\\n---'")
	}
	return r[:idx], bytes.TrimLeft(r[idx+len(closeMark):], "\n\r"), nil
}

// SkillsSeed 是一次 skill 种子导入的结果，供调用方（api 启动 / reseed）做缓存失效。
type SkillsSeed struct {
	Inserted []string // 新插入的 skill code
	Upserted []string // 强制覆盖写入的 skill code（仅 force 语义）
	Pruned   []string // 清理的死行 code（文件里已不存在的内置 skill）
}

// Touched 汇总全部被写动过的 code（insert/upsert/prune 并集）。
func (r SkillsSeed) Touched() []string {
	out := make([]string, 0, len(r.Inserted)+len(r.Upserted)+len(r.Pruned))
	out = append(out, r.Inserted...)
	out = append(out, r.Upserted...)
	out = append(out, r.Pruned...)
	return out
}

// Result 是一次 Import 的整体结果。
type Result struct {
	Agents []string // 写入的 agent code（insert 或 force）
	Skills SkillsSeed
}

// Import 把 dir 下的 agent 和 skill 配置 insert-only 首填进 DB。
// 按 code 判存在→仅不存在才 Create；已存在跳过（前端对配置的修改是 DB 事实源，
// 绝不被启动覆盖）。同时清理文件里已删除的内置 skill 死行（目录改名/删除的残留）。
// 返回写动清单供调用方失效多级缓存。
func Import(
	ctx context.Context,
	dir string,
	h *agent.Store,
	s *skill.Store,
) (Result, error) {
	var res Result
	agents, err := importExecutors(ctx, filepath.Join(dir, "agents"), h, false)
	if err != nil {
		return res, fmt.Errorf("import executors: %w", err)
	}
	res.Agents = agents
	skillsRes, err := importSkills(ctx, filepath.Join(dir, "skills"), s, false)
	if err != nil {
		return res, fmt.Errorf("import skills: %w", err)
	}
	res.Skills = skillsRes
	return res, nil
}

// notFound 判定 GetByCode/GetByID 的「不存在」——store 用 %w 包了 pgx.ErrNoRows。
func notFound(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

// walkFiles 收集 dir 下匹配 ext 的文件路径（字典序），dir 不存在时返回空（种子目录可选）。
func walkFiles(dir, ext string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return fs.SkipDir
			}
			return err
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ext) {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	// WalkDir 已按字典序遍历目录项，out 天然有序。
	return out, nil
}

// importExecutors 扫 dir/*.md，按 code(=frontmatter id) insert-only 建操作员。
func importExecutors(ctx context.Context, dir string, h *agent.Store, force bool) ([]string, error) {
	files, err := walkFiles(dir, ".md")
	if err != nil {
		return nil, err
	}
	updated := make([]string, 0)
	for _, path := range files {
		raw, err := os.ReadFile(path) // #nosec G304 // 路径来自进程配置/种子目录，非用户输入
		if err != nil {
			return nil, fmt.Errorf("读取 %s: %w", path, err)
		}
		front, body, err := splitFrontmatter(raw)
		if err != nil {
			return nil, fmt.Errorf("解析 %s: %w", path, err)
		}
		var f agentFront
		if err := yaml.Unmarshal(front, &f); err != nil {
			return nil, fmt.Errorf("解析 %s frontmatter: %w", path, err)
		}
		code := strings.TrimSpace(f.ID)
		if code == "" {
			return nil, fmt.Errorf("%s: 缺 id", path)
		}
		if _, err := h.GetByCode(ctx, code); err == nil && !force {
			continue // 已存在→跳过（insert-only，DB 是事实源）
		} else if err != nil && !notFound(err) {
			return nil, fmt.Errorf("查操作员 %q: %w", code, err)
		}
		systemPrompt := string(body)
		if _, err := h.Upsert(ctx, code, f.Name, f.Description, systemPrompt, agent.UpdateParams{
			FunctionTools: &f.FunctionTools,
			CliTools:      &f.CliTools,
			Skills:        &f.Skills,
			MaxIterations: &f.MaxIterations,
			Complexity:    strPtr(strings.TrimSpace(f.Complexity)),
		}); err != nil {
			return nil, fmt.Errorf("写操作员 %q: %w", code, err)
		}
		updated = append(updated, code)
	}
	return updated, nil
}

// ImportAgentsForce 把 agents/*.md 强制覆盖写入 DB（reset 语义）：
// 不跳过已存在行，prompt/工具/档位一律以种子为准。
// 返回实际写入的 agent code 列表。常规启动路径仍走 Import（insert-only）。
func ImportAgentsForce(ctx context.Context, dir string, h *agent.Store) ([]string, error) {
	return importExecutors(ctx, filepath.Join(dir, "agents"), h, true)
}

// ImportSkillsForce 把 skills/**/*.md 以强制覆盖语义写入内置行（reset 语义）：
// code 冲突覆盖 name/description/body/category 并复位 enabled。用户自建
// （is_builtin=false）不动。与 ImportAgentsForce 配对，仅供 reseed。
func ImportSkillsForce(ctx context.Context, dir string, s *skill.Store) (SkillsSeed, error) {
	return importSkills(ctx, filepath.Join(dir, "skills"), s, true)
}

func strPtr(s string) *string { return &s }

// skillFront 是 skills/**/*.md (如 skills/tooling/browser-use/SKILL.md) frontmatter 的解析目标。
type skillFront struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Category    string `yaml:"category"` // tooling / vuln
}

// importSkills 扫 dir/**/*.md（递归）同步 skill 表：
//   - code = SKILL.md 所在目录名（裸名，如 browser-use）——与 agent.skills 声明、
//     read_skill 寻址同一命名空间
//   - category = 相对路径首段（tooling/vuln），frontmatter 显式声明优先
//   - insert-only：已存在跳过（前端对内置 skill 的正文/启停修改跨重启保留）；
//     force=true（reseed reset 语义）则 UpsertBuiltin 强制覆盖内置行
//   - 死行清理：文件里已不存在的内置行（目录改名/删除残留）prune 回收；
//     用户自建行永不被 prune。种子目录整体缺失（files 空）时跳过 prune——
//     不能因镜像漏拷种子就把 DB 清空
func importSkills(ctx context.Context, dir string, s *skill.Store, force bool) (SkillsSeed, error) {
	var res SkillsSeed
	files, err := walkFiles(dir, ".md")
	if err != nil {
		return res, err
	}

	type seenSkill struct {
		category string
		sk       skill.Skill
	}
	seen := make(map[string]seenSkill, len(files))

	for _, path := range files {
		raw, err := os.ReadFile(path) // #nosec G304 // 路径来自进程配置/种子目录，非用户输入
		if err != nil {
			return res, fmt.Errorf("读取 %s: %w", path, err)
		}
		front, body, err := splitFrontmatter(raw)
		if err != nil {
			return res, fmt.Errorf("解析 %s: %w", path, err)
		}
		var f skillFront
		if err := yaml.Unmarshal(front, &f); err != nil {
			return res, fmt.Errorf("解析 %s frontmatter: %w", path, err)
		}

		// code = SKILL.md 所在目录名（裸名）；category = 相对路径首段。
		// 例：skills/tooling/browser-use/SKILL.md → code=browser-use, category=tooling
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return res, fmt.Errorf("计算相对路径 %s: %w", path, err)
		}
		segments := strings.Split(filepath.ToSlash(rel), "/")
		if len(segments) < 2 {
			return res, fmt.Errorf("%s: 种子 skill 必须位于 <category>/<name>/SKILL.md 目录结构", path)
		}
		code := segments[len(segments)-2]
		if code == "" {
			return res, fmt.Errorf("%s: skill 目录名为空", path)
		}
		if _, dup := seen[code]; dup {
			return res, fmt.Errorf("skill 裸名冲突: %q 在种子目录中重复出现", code)
		}

		category := f.Category
		if category == "" {
			category = segments[0]
		}

		sk := skill.Skill{
			Code:        code,
			Category:    category,
			Name:        f.Name,
			Description: f.Description,
			Body:        string(body),
			IsBuiltin:   true,
			Enabled:     true,
		}
		seen[code] = seenSkill{category: category, sk: sk}
	}

	keepCodes := make([]string, 0, len(seen))
	for code := range seen {
		keepCodes = append(keepCodes, code)
	}
	sortStrings(keepCodes)

	for _, code := range keepCodes {
		item := seen[code]
		if force {
			// reset 语义：内置行强制覆盖（前端对内置的临时修改被种子重置）。
			if _, err := s.UpsertBuiltin(ctx, item.sk); err != nil {
				return res, fmt.Errorf("强制覆盖 skill %q: %w", code, err)
			}
			res.Upserted = append(res.Upserted, code)
			continue
		}
		if _, err := s.GetByCode(ctx, code); err == nil {
			continue // 已存在→跳过（insert-only，DB 是事实源）
		} else if !notFound(err) {
			return res, fmt.Errorf("查 skill %q: %w", code, err)
		}
		if _, err := s.Create(ctx, item.sk); err != nil {
			return res, fmt.Errorf("建 skill %q: %w", code, err)
		}
		res.Inserted = append(res.Inserted, code)
	}

	// 死行清理：仅在种子目录有文件时执行（目录缺失≠全部删除）。
	if len(files) > 0 {
		pruned, err := s.PruneBuiltinNotIn(ctx, keepCodes)
		if err != nil {
			return res, fmt.Errorf("清理内置 skill 死行: %w", err)
		}
		res.Pruned = pruned
	}
	return res, nil
}

// sortStrings 简易排序（避免引入 sort 包；切片小开销可忽略）。
func sortStrings(ss []string) {
	for i := 1; i < len(ss); i++ {
		for j := i; j > 0 && ss[j-1] > ss[j]; j-- {
			ss[j-1], ss[j] = ss[j], ss[j-1]
		}
	}
}
