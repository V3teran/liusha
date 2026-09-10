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
// id 用作稳定引用键 code；kind∈{planner,executor,evaluator}；body 取 markdown 正文。
// function_tools 是内置函数工具（进程内原生函数 code 列表）。
// cli_tools 是外置 CLI 工具集（tools.yaml 名字），严格白名单，空=不装配任何外部工具。
// skills 是 Agent 可访问的 Skill code 列表（如 ["tooling/browser-use", "vuln/dom-xss"]）。
type agentFront struct {
	ID            string   `yaml:"id"`
	Name          string   `yaml:"name"`
	Description   string   `yaml:"description"`
	Kind          string   `yaml:"kind"`
	FunctionTools []string `yaml:"function_tools"`
	CliTools      []string `yaml:"cli_tools"`
	Skills        []string `yaml:"skills"`
	MaxIterations int      `yaml:"max_iterations"`
	Tier          string   `yaml:"tier"` // 能力档 heavy|vision|light（空 → store 落 DEFAULT 'heavy'）
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

// Import 把 dir 下的 agent 和 skill 配置 insert-only 首填进 DB。
// 按 code 判存在→仅不存在才 Create；已存在跳过（绝不覆盖 DB 事实源）。
func Import(
	ctx context.Context,
	dir string,
	h *agent.Store,
	s *skill.Store,
) error {
	if err := importExecutors(ctx, filepath.Join(dir, "agents"), h); err != nil {
		return fmt.Errorf("import executors: %w", err)
	}
	if err := importSkills(ctx, filepath.Join(dir, "skills"), s); err != nil {
		return fmt.Errorf("import skills: %w", err)
	}
	return nil
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
func importExecutors(ctx context.Context, dir string, h *agent.Store) error {
	files, err := walkFiles(dir, ".md")
	if err != nil {
		return err
	}
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("读取 %s: %w", path, err)
		}
		front, body, err := splitFrontmatter(raw)
		if err != nil {
			return fmt.Errorf("解析 %s: %w", path, err)
		}
		var f agentFront
		if err := yaml.Unmarshal(front, &f); err != nil {
			return fmt.Errorf("解析 %s frontmatter: %w", path, err)
		}
		code := strings.TrimSpace(f.ID)
		if code == "" {
			return fmt.Errorf("%s: 缺 id", path)
		}
		if _, err := h.GetByCode(ctx, code); err == nil {
			continue // 已存在→跳过（insert-only）
		} else if !notFound(err) {
			return fmt.Errorf("查操作员 %q: %w", code, err)
		}
		systemPrompt := string(body)
		if _, err := h.Update(ctx, code, agent.UpdateParams{
			SystemPrompt:  &systemPrompt,
			FunctionTools: &f.FunctionTools,
			CliTools:      &f.CliTools,
			Skills:        &f.Skills,
			MaxIterations: &f.MaxIterations,
			Complexity:    strPtr(strings.TrimSpace(f.Tier)),
		}); err != nil {
			return fmt.Errorf("建操作员 %q: %w", code, err)
		}
	}
	return nil
}

func strPtr(s string) *string { return &s }

// skillFront 是 skills/**/*.md (如 skills/tooling/browser-use/SKILL.md) frontmatter 的解析目标。
type skillFront struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Category    string `yaml:"category"` // tooling / vuln
}

// importSkills 扫 dir/**/*.md（递归），按 code(=相对路径去.md) insert-only 建 Skill。
// 例如：skills/tooling/browser-use/SKILL.md → code="tooling/browser-use"
func importSkills(ctx context.Context, dir string, s *skill.Store) error {
	files, err := walkFiles(dir, ".md")
	if err != nil {
		return err
	}
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("读取 %s: %w", path, err)
		}
		front, body, err := splitFrontmatter(raw)
		if err != nil {
			return fmt.Errorf("解析 %s: %w", path, err)
		}
		var f skillFront
		if err := yaml.Unmarshal(front, &f); err != nil {
			return fmt.Errorf("解析 %s frontmatter: %w", path, err)
		}

		// code = 相对 dir 的路径去掉 /SKILL.md 后缀
		// 例如：skills/tooling/browser-use/SKILL.md → tooling/browser-use
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return fmt.Errorf("计算相对路径 %s: %w", path, err)
		}
		code := strings.TrimSuffix(rel, "/SKILL.md")
		code = strings.TrimSuffix(code, "\\SKILL.md") // Windows
		code = filepath.ToSlash(code)                 // 统一使用 / 分隔符

		// 推断 category（从 code 第一段提取，如 tooling/browser-use → tooling）
		category := f.Category
		if category == "" {
			parts := strings.Split(code, "/")
			if len(parts) > 0 {
				category = parts[0]
			}
		}

		// 检查是否已存在
		if _, err := s.GetByCode(ctx, code); err == nil {
			continue // 已存在→跳过（insert-only）
		} else if !notFound(err) {
			return fmt.Errorf("查 skill %q: %w", code, err)
		}

		// 创建 Skill
		skill := skill.Skill{
			Code:        code,
			Category:    category,
			Name:        f.Name,
			Description: f.Description,
			Body:        string(body),
			IsBuiltin:   true,
			Enabled:     true,
		}
		if _, err := s.Create(ctx, skill); err != nil {
			return fmt.Errorf("建 skill %q: %w", code, err)
		}
	}
	return nil
}
