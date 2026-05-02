package skill

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// Loader 从 root 目录加载 SKILL.md，提供 CC 风格的渐进式加载：
//
//	Index()           启动时 walk root，预解析所有 SKILL.md 的 frontmatter（不读 body）→ 进 metaCache
//	Load(name)        懒加载完整 Card：未命中 → ReadFile + 解析 + 校验 + 缓存进 cardCache
//	                  命中 cardCache 直接返回（每 spawn_skill 调用 0 文件 IO）
//
// 启动校验：
//  1. frontmatter 解析成功
//  2. cognitive_map（如配置）文件存在 + 含 ≥ 6 个 ^##\s+\d+\. 标题（黑客松 6 槽位）
//  3. done_validator（如配置）必须在 ActionRegistry 已注册（通过回调判断）
//
// 设计要点：
//   - 启动时 Index() 把所有 SKILL.md 的 frontmatter 全扫一遍——发现配置错误 fail-fast，
//     而不是等到 spawn_skill("xxx") 才报。
//   - Load() 第一次按需读 body 后写入 cardCache；后续命中直接返回。
//   - 并发安全：sync.Map 双层缓存。
type Loader struct {
	root      string
	metaCache sync.Map // key=name, val=*Card（metadata only, Body 为空）
	cardCache sync.Map // key=name, val=*Card（含 Body，已校验完毕）
}

// NewLoader 构造 Loader；root 通常来自 cfg.Skills.Root。
func NewLoader(root string) *Loader {
	return &Loader{root: root}
}

// Index 启动时遍历 root，把每个 <name>/SKILL.md 的 frontmatter 解析进 metaCache。
//
// CC 风格：metadata always loaded（启动期校验 + 路由可见），body lazy load。
//
// 返回：发现的 skill 名列表（按字典序）。任一 SKILL.md 解析失败 → 立即 error，启动 fail-fast。
//
// 注意：该方法不校验 cognitive_map / done_validator——这些校验在 Load 时按需做，
// 因为 done_validator 注册依赖 tool.Registry，而 Index 在 main 装配早期跑（Registry 还未填）。
func (l *Loader) Index() ([]string, error) {
	var names []string
	err := filepath.WalkDir(l.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() != "SKILL.md" {
			return nil
		}
		// name = path 相对 root 的目录（去掉 /SKILL.md 后缀）
		rel, err := filepath.Rel(l.root, filepath.Dir(path))
		if err != nil {
			return fmt.Errorf("rel(%s,%s): %w", l.root, path, err)
		}
		name := filepath.ToSlash(rel)

		raw, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("读取 %s: %w", path, err)
		}
		front, _, err := splitFrontmatter(raw)
		if err != nil {
			return fmt.Errorf("解析 %s: %w", path, err)
		}
		var meta Card
		if err := yaml.Unmarshal(front, &meta); err != nil {
			return fmt.Errorf("yaml 解析 %s: %w", path, err)
		}
		// metaCache 不含 Body，省内存（多 skill 时尤其重要）
		l.metaCache.Store(name, &meta)
		names = append(names, name)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sortStrings(names)
	return names, nil
}

// Load 读 root/<name>/SKILL.md，解析 frontmatter + body，返回完整 Card。
//
// 命中 cardCache 直接返回；未命中 → 读 SKILL.md + 缓存。
//
// 设计简化（v1.1 末次精简）：
//   - 不再有启动期 cross-check（allowed-tools / done_validator 字段已删）
//   - builder 是唯一真理来源：register 什么工具就能用什么；NewBACValidator 直接装配
func (l *Loader) Load(name string) (*Card, error) {
	if v, ok := l.cardCache.Load(name); ok {
		return v.(*Card), nil
	}

	full := filepath.Join(l.root, name, "SKILL.md")
	raw, err := os.ReadFile(full)
	if err != nil {
		return nil, fmt.Errorf("读取 %s: %w", full, err)
	}

	front, body, err := splitFrontmatter(raw)
	if err != nil {
		return nil, fmt.Errorf("解析 %s: %w", full, err)
	}

	var card Card
	if err := yaml.Unmarshal(front, &card); err != nil {
		return nil, fmt.Errorf("yaml 解析 %s: %w", full, err)
	}
	card.Body = string(body)

	l.cardCache.Store(name, &card)
	return &card, nil
}

// MetaOnly 返回 metaCache 中已加载的 Card 副本（仅 frontmatter，Body 为空）。
// 路由层可用此快速决定 spawn 哪个 skill 而不触发 body 读盘。
func (l *Loader) MetaOnly(name string) (*Card, bool) {
	v, ok := l.metaCache.Load(name)
	if !ok {
		return nil, false
	}
	c := *v.(*Card)
	return &c, true
}

// List 返回所有已 Index 的 skill 元数据列表（仅 frontmatter，Body 为空）。
// 主 ReAct 用此构造 system prompt catalog —— LLM 自动看到所有可用 skill。
//
// CC 风格关键 API：调用方加 SKILL.md 文件 + 注册 builder = skill 立即可被 LLM 发现。
func (l *Loader) List() []*Card {
	var out []*Card
	l.metaCache.Range(func(_, v any) bool {
		c := *v.(*Card)
		out = append(out, &c)
		return true
	})
	return out
}

var (
	frontmatterDelim = []byte("---")
	newline          = []byte("\n")
)

// splitFrontmatter 把 SKILL.md 切成 (frontmatter, body)。
//
// 协议：文件开头（允许前置空白/换行）必须是 ---\n，然后 yaml，然后 \n--- 结束。
// 这是 Jekyll/Hugo 等通用约定。
func splitFrontmatter(raw []byte) ([]byte, []byte, error) {
	r := bytes.TrimLeft(raw, "\n\r\t ")
	if !bytes.HasPrefix(r, frontmatterDelim) {
		return nil, nil, errors.New("missing frontmatter: 文件开头未发现 '---' 分隔")
	}
	r = r[len(frontmatterDelim):]

	closeMark := append(append([]byte{}, newline...), frontmatterDelim...)
	idx := bytes.Index(r, closeMark)
	if idx < 0 {
		return nil, nil, errors.New("frontmatter not closed: 未发现闭合的 '\\n---'")
	}
	front := r[:idx]
	body := r[idx+len(closeMark):]
	return front, bytes.TrimLeft(body, "\n\r"), nil
}

// sortStrings 简易排序（避免引入 sort 包；切片小开销可忽略）。
func sortStrings(ss []string) {
	for i := 1; i < len(ss); i++ {
		for j := i; j > 0 && strings.Compare(ss[j-1], ss[j]) > 0; j-- {
			ss[j-1], ss[j] = ss[j], ss[j-1]
		}
	}
}
