package einoagent

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// role.go：liusha 自有的 hunter 角色动态加载（杀伤链阶段 sub-agent + orchestrator）。
//
// 设计（对话式平台规划，见 docs/superpowers/specs/2026-06-07-conversational-platform.md）：
//   - 角色定义 = 一个 markdown 文件（frontmatter 元信息 + body 系统提示），运行时扫目录加载
//   - 借鉴「目录扫 markdown + frontmatter」的通用模式（Claude Code subagent 等），但 frontmatter
//     字段是 liusha 自有（tools 指向 liusha einotools 名、kind 区分 deep 主/子代理）——与任何
//     现成实现无关
//   - 工具用「名字清单」声明，运行时由工具注册表（见 role_tools.go）按 owner/host/sandbox 注入实例
//
// 与 internal/skill.Loader 的区别：skill 是 <name>/SKILL.md 子目录（漏洞/工具手册），
// role 是扁平 <id>.md（agent 角色），字段/语义都不同，故独立实现而非复用。

// RoleKind 区分 deep 编排里的主代理与子代理。
type RoleKind string

const (
	// RoleOrchestrator 是 deep 主代理（commander/指挥官），负责拆活派 sub-agent。
	RoleOrchestrator RoleKind = "orchestrator"
	// RoleSubAgent 是杀伤链阶段子代理（recon / striker / ...），被主代理 task 委派。
	RoleSubAgent RoleKind = "subagent"
)

// RoleDef 是一个角色 markdown 解析后的内存形态。
type RoleDef struct {
	ID            string   `yaml:"id"`             // 角色标识（striker / recon / commander），全局唯一
	Name          string   `yaml:"name"`           // 显示名（中文友好）
	Description   string   `yaml:"description"`    // 给 deep task 工具：主代理据此决定派给谁（必填）
	Kind          RoleKind `yaml:"kind"`           // orchestrator | subagent；空视为 subagent
	Tools         []string `yaml:"tools"`          // 工具名清单（从 liusha einotools 选）；空=不挂工具
	MaxIterations int      `yaml:"max_iterations"` // 0=用默认
	SystemPrompt  string   `yaml:"-"`              // markdown body（角色系统提示）
	SourceFile    string   `yaml:"-"`              // 来源文件路径（诊断用）
}

// LoadRoles 扫 dir 下所有 *.md，解析成 RoleDef 列表（按 id 字典序）。
// 任一文件解析失败 / id 重复 / 必填缺失 → 立即 error（启动 fail-fast）。
func LoadRoles(dir string) ([]RoleDef, error) {
	var roles []RoleDef
	seen := map[string]string{} // id → file（查重）

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("读取 %s: %w", path, err)
		}
		role, err := parseRole(raw)
		if err != nil {
			return fmt.Errorf("解析角色 %s: %w", path, err)
		}
		role.SourceFile = path
		if prev, dup := seen[role.ID]; dup {
			return fmt.Errorf("角色 id %q 重复：%s 与 %s", role.ID, prev, path)
		}
		seen[role.ID] = path
		roles = append(roles, role)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sortRolesByID(roles)
	return roles, nil
}

// parseRole 把一个角色 markdown 切成 frontmatter + body，校验必填。
func parseRole(raw []byte) (RoleDef, error) {
	front, body, err := splitRoleFrontmatter(raw)
	if err != nil {
		return RoleDef{}, err
	}
	var role RoleDef
	if err := yaml.Unmarshal(front, &role); err != nil {
		return RoleDef{}, fmt.Errorf("yaml: %w", err)
	}
	role.ID = strings.TrimSpace(role.ID)
	role.Description = strings.TrimSpace(role.Description)
	role.SystemPrompt = string(body)
	if role.Kind == "" {
		role.Kind = RoleSubAgent
	}

	// 校验：id / description 必填（description 是 deep task 工具派活的依据，不能空）。
	if role.ID == "" {
		return RoleDef{}, errors.New("缺 id")
	}
	if role.Description == "" {
		return RoleDef{}, errors.New("缺 description（deep task 工具据此派活，必填）")
	}
	if role.Kind != RoleOrchestrator && role.Kind != RoleSubAgent {
		return RoleDef{}, fmt.Errorf("kind 非法 %q（仅 orchestrator | subagent）", role.Kind)
	}
	return role, nil
}

// Orchestrator 从角色列表里挑出唯一的主代理（commander）。多于一个 / 没有都报错。
func Orchestrator(roles []RoleDef) (RoleDef, error) {
	var found []RoleDef
	for _, r := range roles {
		if r.Kind == RoleOrchestrator {
			found = append(found, r)
		}
	}
	if len(found) == 0 {
		return RoleDef{}, errors.New("无 orchestrator 角色（需恰好一个 kind: orchestrator）")
	}
	if len(found) > 1 {
		return RoleDef{}, fmt.Errorf("orchestrator 角色多于一个：%s, %s", found[0].SourceFile, found[1].SourceFile)
	}
	return found[0], nil
}

// SubAgents 返回所有子代理角色（杀伤链阶段）。
func SubAgents(roles []RoleDef) []RoleDef {
	var out []RoleDef
	for _, r := range roles {
		if r.Kind == RoleSubAgent {
			out = append(out, r)
		}
	}
	return out
}

var (
	roleFrontDelim = []byte("---")
	roleNewline    = []byte("\n")
)

// splitRoleFrontmatter 切角色 markdown 成 (frontmatter, body)。约定同 Jekyll/Hugo：开头 ---\n yaml \n---。
func splitRoleFrontmatter(raw []byte) ([]byte, []byte, error) {
	r := bytes.TrimLeft(raw, "\n\r\t ")
	if !bytes.HasPrefix(r, roleFrontDelim) {
		return nil, nil, errors.New("缺 frontmatter：开头未发现 '---'")
	}
	r = r[len(roleFrontDelim):]
	closeMark := append(append([]byte{}, roleNewline...), roleFrontDelim...)
	idx := bytes.Index(r, closeMark)
	if idx < 0 {
		return nil, nil, errors.New("frontmatter 未闭合：缺 '\\n---'")
	}
	return r[:idx], bytes.TrimLeft(r[idx+len(closeMark):], "\n\r"), nil
}

// sortRolesByID 按 id 字典序排（小切片插排，省 sort 包）。
func sortRolesByID(rs []RoleDef) {
	for i := 1; i < len(rs); i++ {
		for j := i; j > 0 && rs[j-1].ID > rs[j].ID; j-- {
			rs[j-1], rs[j] = rs[j], rs[j-1]
		}
	}
}
