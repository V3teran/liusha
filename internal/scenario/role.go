// Package scenario 实现「场景 role」——用户在前端会话选的扫描场景（Web 渗透 / CTF / 被动侦察…）。
//
// 与 einoagent 的杀伤链角色（hunters/*.md：orchestrator 主代理 + reconnaissance/exploitation subagent，deep 装配用）
// 正交（见记忆 project_phaseb_sse_arch / reference_eino_vs_adk 的两层角色理解）：
//   - 场景 role（本包，scenarios/*.md）：换主代理的人设侧重 + 决定 active/passive 模式，**不改杀伤链结构**
//   - 杀伤链 role（einoagent，hunters/*.md）：deep 的固定 sub-agent 模板，所有场景共用
//
// 动态加载：扫 scenarios/*.md，frontmatter 元信息 + body 人设 addendum（注入 orchestrator/traffic-analysis prompt）。
// 借鉴 CyberStrikeAI 的 roles 理念（场景人设），实现自研（frontmatter 字段 liusha 自有）。
package scenario

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Mode 区分场景的运行模式。
type Mode string

const (
	// ModeActive：会话发起，deep 编排（orchestrator + 杀伤链 sub-agents）。
	ModeActive Mode = "active"
	// ModePassive：流量自动驱动，trafficAnalysis 单 agent，不会话。
	ModePassive Mode = "passive"
)

// Role 是一个场景 role markdown 解析后的内存形态。
type Role struct {
	ID           string `yaml:"id"`          // 场景标识（web-pentest / passive-recon），全局唯一
	Name         string `yaml:"name"`        // 显示名（前端选择列表用）
	Description  string `yaml:"description"` // 场景说明（前端选择时展示，必填）
	Mode         Mode   `yaml:"mode"`        // active | passive
	SystemPrompt string `yaml:"-"`           // markdown body：人设 addendum，注入主代理 prompt
	SourceFile   string `yaml:"-"`           // 来源文件路径（诊断用）
}

// LoadRoles 扫 dir 下所有 *.md，解析成 Role 列表（按 id 字典序）。
// 任一文件解析失败 / id 重复 / 必填缺失 / mode 非法 → 立即 error（启动 fail-fast）。
func LoadRoles(dir string) ([]Role, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("读取场景 role 目录 %s: %w", dir, err)
	}
	var roles []Role
	seen := map[string]string{} // id → sourceFile，查重
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("读取 %s: %w", path, err)
		}
		role, err := parseRole(raw)
		if err != nil {
			return nil, fmt.Errorf("解析 %s: %w", path, err)
		}
		role.SourceFile = path
		if prev, dup := seen[role.ID]; dup {
			return nil, fmt.Errorf("场景 role id %q 重复：%s 与 %s", role.ID, prev, path)
		}
		seen[role.ID] = path
		roles = append(roles, role)
	}
	sort.Slice(roles, func(i, j int) bool { return roles[i].ID < roles[j].ID })
	return roles, nil
}

// parseRole 拆 frontmatter + body，校验必填与 mode 合法性。
func parseRole(raw []byte) (Role, error) {
	front, body, err := splitFrontmatter(raw)
	if err != nil {
		return Role{}, err
	}
	var role Role
	if err := yaml.Unmarshal(front, &role); err != nil {
		return Role{}, fmt.Errorf("yaml: %w", err)
	}
	role.SystemPrompt = strings.TrimSpace(string(body))
	if role.ID == "" {
		return Role{}, errors.New("缺 id")
	}
	if role.Description == "" {
		return Role{}, errors.New("缺 description（前端选择列表据此展示，必填）")
	}
	if role.Mode != ModeActive && role.Mode != ModePassive {
		return Role{}, fmt.Errorf("mode 非法 %q（仅 active | passive）", role.Mode)
	}
	return role, nil
}

// splitFrontmatter 拆 `---\n<yaml>\n---\n<body>`。无 frontmatter 视为格式错误。
func splitFrontmatter(raw []byte) (front, body []byte, err error) {
	trimmed := bytes.TrimLeft(raw, " \t\r\n")
	if !bytes.HasPrefix(trimmed, []byte("---")) {
		return nil, nil, errors.New("缺 frontmatter（需以 --- 开头）")
	}
	rest := trimmed[3:]
	idx := bytes.Index(rest, []byte("\n---"))
	if idx < 0 {
		return nil, nil, errors.New("frontmatter 未闭合（缺第二个 ---）")
	}
	front = rest[:idx]
	after := rest[idx+4:] // 跳过 "\n---"
	if nl := bytes.IndexByte(after, '\n'); nl >= 0 {
		body = after[nl+1:]
	}
	return front, body, nil
}

// ByID 按 id 查场景 role。
func ByID(roles []Role, id string) (Role, bool) {
	for _, r := range roles {
		if r.ID == id {
			return r, true
		}
	}
	return Role{}, false
}

// DefaultForMode 返回某 mode 下字典序第一个 role（caller 未指定 role 时的兜底）。
func DefaultForMode(roles []Role, mode Mode) (Role, bool) {
	for _, r := range roles {
		if r.Mode == mode {
			return r, true
		}
	}
	return Role{}, false
}

// FilterByMode 返回某 mode 的所有 role（前端按模式分组展示）。
func FilterByMode(roles []Role, mode Mode) []Role {
	var out []Role
	for _, r := range roles {
		if r.Mode == mode {
			out = append(out, r)
		}
	}
	return out
}
