// Package builtin 承载随程序发布的出厂内容：四个 agent 的出厂配置与内置技能。
//
// 内容编译进程序（go:embed），任何进程读到的出厂值都与自身代码同一版本——
// 章程引用的工具、技能与代码永远对得上，不存在启动同步与版本错配。
// 用户在前端的修改是叠加在出厂值之上的自定义层（见 internal/override），
// 不写回本包。
//
// 目录约定：
//   - agents/<code>.md            ：frontmatter（出厂配置）+ 正文（章程）
//   - skills/<category>/<code>/SKILL.md：frontmatter（名称/描述）+ 正文
//
// 章程只承载可自定义的角色指导；程序解析所依赖的输出契约随解析代码放在各
// agent 包内（planner/executor/evaluator 的 contract.md），不在这里。
package builtin

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

//go:embed agents/*.md
var agentFS embed.FS

//go:embed skills
var skillFS embed.FS

// Agent 是一个 agent 角色的出厂配置。
type Agent struct {
	Code          string   // 角色身份（planner/executor/evaluator/monitor）
	Name          string   // 显示名
	Description   string   // 调度摘要：planner 规划时读取的角色分工
	Charter       string   // 章程：角色定位、原则、策略（可自定义）
	FunctionTools []string // 内置函数工具白名单（"*" = 全部）
	CliTools      []string // 沙箱 CLI 工具白名单（"*" = 全部）
	Skills        []string // 技能白名单（"*" = 全部）
	MaxIterations int      // ReAct 迭代上限
	Complexity    string   // LLM 档位：simple | medium | complex
}

// Skill 是一个内置技能的出厂内容。Code 取 SKILL.md 所在目录名，
// Category 取其上一级目录名（vuln = 漏洞知识 / tool = 工具使用知识）。
type Skill struct {
	Code        string
	Category    string
	Name        string
	Description string
	Body        string
}

type agentFront struct {
	Code          string   `yaml:"code"`
	Name          string   `yaml:"name"`
	Description   string   `yaml:"description"`
	FunctionTools []string `yaml:"function_tools"`
	CliTools      []string `yaml:"cli_tools"`
	Skills        []string `yaml:"skills"`
	MaxIterations int      `yaml:"max_iterations"`
	Complexity    string   `yaml:"complexity"`
}

type skillFront struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

type catalog struct {
	agents []Agent
	skills []Skill
}

// load 解析全部出厂内容。内容随编译固定，解析失败是构建产物缺陷——
// 单元测试保证其不发生，运行期遇到即 panic（同模板 Must 语义）。
var load = sync.OnceValue(func() catalog {
	agents, err := parseAgents(agentFS)
	if err != nil {
		panic(fmt.Sprintf("builtin: 解析出厂 agent 失败: %v", err))
	}
	skills, err := parseSkills(skillFS)
	if err != nil {
		panic(fmt.Sprintf("builtin: 解析内置技能失败: %v", err))
	}
	return catalog{agents: agents, skills: skills}
})

// Agents 返回全部出厂 agent（按 code 升序，返回副本）。
func Agents() []Agent {
	src := load().agents
	out := make([]Agent, len(src))
	for i, a := range src {
		out[i] = a.clone()
	}
	return out
}

// AgentByCode 按角色 code 取出厂 agent。
func AgentByCode(code string) (Agent, bool) {
	for _, a := range load().agents {
		if a.Code == code {
			return a.clone(), true
		}
	}
	return Agent{}, false
}

// Skills 返回全部内置技能（按 category、code 升序，返回副本）。
func Skills() []Skill {
	return slices.Clone(load().skills)
}

// SkillByCode 按 code 取内置技能。
func SkillByCode(code string) (Skill, bool) {
	for _, s := range load().skills {
		if s.Code == code {
			return s, true
		}
	}
	return Skill{}, false
}

func (a Agent) clone() Agent {
	a.FunctionTools = slices.Clone(a.FunctionTools)
	a.CliTools = slices.Clone(a.CliTools)
	a.Skills = slices.Clone(a.Skills)
	return a
}

func parseAgents(fsys fs.FS) ([]Agent, error) {
	paths, err := fs.Glob(fsys, "agents/*.md")
	if err != nil {
		return nil, err
	}
	out := make([]Agent, 0, len(paths))
	for _, p := range paths {
		raw, err := fs.ReadFile(fsys, p)
		if err != nil {
			return nil, err
		}
		front, body, err := SplitFrontmatter(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		var f agentFront
		if err := yaml.Unmarshal(front, &f); err != nil {
			return nil, fmt.Errorf("%s: frontmatter: %w", p, err)
		}
		if want := strings.TrimSuffix(path.Base(p), ".md"); f.Code != want {
			return nil, fmt.Errorf("%s: frontmatter code %q 与文件名不一致", p, f.Code)
		}
		out = append(out, Agent{
			Code:          f.Code,
			Name:          f.Name,
			Description:   f.Description,
			Charter:       string(body),
			FunctionTools: orEmpty(f.FunctionTools),
			CliTools:      orEmpty(f.CliTools),
			Skills:        orEmpty(f.Skills),
			MaxIterations: f.MaxIterations,
			Complexity:    f.Complexity,
		})
	}
	slices.SortFunc(out, func(a, b Agent) int { return strings.Compare(a.Code, b.Code) })
	return out, nil
}

func parseSkills(fsys fs.FS) ([]Skill, error) {
	paths, err := fs.Glob(fsys, "skills/*/*/SKILL.md")
	if err != nil {
		return nil, err
	}
	out := make([]Skill, 0, len(paths))
	for _, p := range paths {
		raw, err := fs.ReadFile(fsys, p)
		if err != nil {
			return nil, err
		}
		front, body, err := SplitFrontmatter(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		var f skillFront
		if err := yaml.Unmarshal(front, &f); err != nil {
			return nil, fmt.Errorf("%s: frontmatter: %w", p, err)
		}
		dir := path.Dir(p)
		out = append(out, Skill{
			Code:        path.Base(dir),
			Category:    path.Base(path.Dir(dir)),
			Name:        f.Name,
			Description: f.Description,
			Body:        string(body),
		})
	}
	slices.SortFunc(out, func(a, b Skill) int {
		if c := strings.Compare(a.Category, b.Category); c != 0 {
			return c
		}
		return strings.Compare(a.Code, b.Code)
	})
	return out, nil
}

// SplitFrontmatter 把 markdown 切成 (frontmatter, 正文)：开头 `---` 行与下一个
// `---` 行之间为 YAML，其后为正文（去掉紧随的空行）。
func SplitFrontmatter(raw []byte) (front, body []byte, err error) {
	r := bytes.TrimLeft(raw, "\n\r\t ")
	if !bytes.HasPrefix(r, []byte("---")) {
		return nil, nil, errors.New("缺 frontmatter：开头未发现 '---'")
	}
	r = r[len("---"):]
	idx := bytes.Index(r, []byte("\n---"))
	if idx < 0 {
		return nil, nil, errors.New("frontmatter 未闭合：缺 '\\n---'")
	}
	return r[:idx], bytes.TrimLeft(r[idx+len("\n---"):], "\n\r"), nil
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
