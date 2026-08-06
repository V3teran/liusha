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

// hunter.go：liusha einoagent 侧的猎手定义内存形态与解析。
//
// 设计：一个猎手定义 = 一段 frontmatter（元信息）+ body（系统提示）。运行期 DB 是事实源
// （见 internal/config/* + configstore）；本文件的解析器仅服务测试与工具装配的内存表示，
// 不再在生产运行期扫目录加载——runner 从 configstore 取猎手，映射成本包的 HunterDef。
//
// 词汇：einoagent 侧猎手 kind ∈ {orchestrator, subagent, solo}；配置层（cfghunter）用
// {orchestrator, domain}。两侧不同物，跨层由 handler 的映射函数翻译（见 M5）。

// HunterKind 区分 deep 编排里的主代理、子代理与单代理。
type HunterKind string

const (
	// HunterOrchestrator 是 deep 主代理（编排者），负责拆活派子代理。
	HunterOrchestrator HunterKind = "orchestrator"
	// HunterSubAgent 是 swarm 引擎下被主代理 task 委派的领域子代理。
	HunterSubAgent HunterKind = "subagent"
	// HunterSolo 是 solo 引擎下单独跑的猎手（无编排者，body 直接拼成 system 指令）。
	HunterSolo HunterKind = "solo"
)

// HunterDef 是一个猎手定义解析后的内存形态。
type HunterDef struct {
	ID            string     `yaml:"id"`             // 猎手标识（exploitation / recon / orchestrator），全局唯一
	Name          string     `yaml:"name"`           // 显示名（中文友好）
	Description   string     `yaml:"description"`    // 给 deep task 工具：主代理据此决定派给谁（必填）
	Kind          HunterKind `yaml:"kind"`           // orchestrator | subagent | solo；空视为 subagent
	FunctionTools []string   `yaml:"function_tools"` // 内置函数工具名清单（从 liusha einotools 选）；空=不挂工具
	MaxIterations int        `yaml:"max_iterations"` // 0=用默认
	SystemPrompt  string     `yaml:"-"`              // markdown body（猎手系统提示）
	SourceFile    string     `yaml:"-"`              // 来源文件路径（诊断用；DB 事实源下可空）
}

// LoadHunters 扫 dir 下所有 *.md，解析成 HunterDef 列表（按 id 字典序）。
// 任一文件解析失败 / id 重复 / 必填缺失 → 立即 error（fail-fast）。
func LoadHunters(dir string) ([]HunterDef, error) {
	var hunters []HunterDef
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
		h, err := parseHunter(raw)
		if err != nil {
			return fmt.Errorf("解析猎手 %s: %w", path, err)
		}
		h.SourceFile = path
		if prev, dup := seen[h.ID]; dup {
			return fmt.Errorf("猎手 id %q 重复：%s 与 %s", h.ID, prev, path)
		}
		seen[h.ID] = path
		hunters = append(hunters, h)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sortHuntersByID(hunters)
	return hunters, nil
}

// parseHunter 把一个猎手 markdown 切成 frontmatter + body，校验必填。
func parseHunter(raw []byte) (HunterDef, error) {
	front, body, err := splitHunterFrontmatter(raw)
	if err != nil {
		return HunterDef{}, err
	}
	var h HunterDef
	if err := yaml.Unmarshal(front, &h); err != nil {
		return HunterDef{}, fmt.Errorf("yaml: %w", err)
	}
	h.ID = strings.TrimSpace(h.ID)
	h.Description = strings.TrimSpace(h.Description)
	h.SystemPrompt = string(body)
	if h.Kind == "" {
		h.Kind = HunterSubAgent
	}

	// 校验：id / description 必填（description 是 deep task 工具派活的依据，不能空）。
	if h.ID == "" {
		return HunterDef{}, errors.New("缺 id")
	}
	if h.Description == "" {
		return HunterDef{}, errors.New("缺 description（deep task 工具据此派活，必填）")
	}
	switch h.Kind {
	case HunterOrchestrator, HunterSubAgent, HunterSolo:
		// ok
	default:
		return HunterDef{}, fmt.Errorf("kind 非法 %q（应为 orchestrator|subagent|solo）", h.Kind)
	}
	return h, nil
}

// Orchestrator 从猎手列表里挑出唯一的主代理（orchestrator）。多于一个 / 没有都报错。
func Orchestrator(hunters []HunterDef) (HunterDef, error) {
	var found []HunterDef
	for _, h := range hunters {
		if h.Kind == HunterOrchestrator {
			found = append(found, h)
		}
	}
	if len(found) == 0 {
		return HunterDef{}, errors.New("无 orchestrator 猎手（需恰好一个 kind: orchestrator）")
	}
	if len(found) > 1 {
		return HunterDef{}, fmt.Errorf("orchestrator 猎手多于一个：%s, %s", found[0].SourceFile, found[1].SourceFile)
	}
	return found[0], nil
}

// SubAgents 返回所有子代理猎手（swarm 杀伤链阶段）。
func SubAgents(hunters []HunterDef) []HunterDef {
	var out []HunterDef
	for _, h := range hunters {
		if h.Kind == HunterSubAgent {
			out = append(out, h)
		}
	}
	return out
}

var (
	hunterFrontDelim = []byte("---")
	hunterNewline    = []byte("\n")
)

// splitHunterFrontmatter 切猎手 markdown 成 (frontmatter, body)。约定同 Jekyll/Hugo：开头 ---\n yaml \n---。
func splitHunterFrontmatter(raw []byte) ([]byte, []byte, error) {
	r := bytes.TrimLeft(raw, "\n\r\t ")
	if !bytes.HasPrefix(r, hunterFrontDelim) {
		return nil, nil, errors.New("缺 frontmatter：开头未发现 '---'")
	}
	r = r[len(hunterFrontDelim):]
	closeMark := append(append([]byte{}, hunterNewline...), hunterFrontDelim...)
	idx := bytes.Index(r, closeMark)
	if idx < 0 {
		return nil, nil, errors.New("frontmatter 未闭合：缺 '\\n---'")
	}
	return r[:idx], bytes.TrimLeft(r[idx+len(closeMark):], "\n\r"), nil
}

// sortHuntersByID 按 id 字典序排（小切片插排，省 sort 包）。
func sortHuntersByID(hs []HunterDef) {
	for i := 1; i < len(hs); i++ {
		for j := i; j > 0 && hs[j-1].ID > hs[j].ID; j-- {
			hs[j-1], hs[j] = hs[j], hs[j-1]
		}
	}
}
