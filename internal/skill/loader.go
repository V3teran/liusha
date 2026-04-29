package skill

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"gopkg.in/yaml.v3"
)

// Loader 从 root 目录加载 SKILL.md。
//
// 启动时校验：
//  1. frontmatter 解析成功
//  2. cognitive_map（如配置）文件存在 + 含 ≥ 6 个 ^##\s+\d+\. 标题（黑客松 6 槽位）
//  3. done_validator（如配置）必须在 ActionRegistry 已注册（通过回调判断）
type Loader struct {
	root string
}

// NewLoader 构造 Loader；root 通常来自 cfg.Skills.Root。
func NewLoader(root string) *Loader {
	return &Loader{root: root}
}

// Load 读 root/<name>/SKILL.md，解析 frontmatter + body，跑校验，返回 Card。
//
// doneValidatorRegistered 由调用方注入（一般指向 action.Registry.HasDoneValidator）；
// 这样 skill 包不必反向依赖 action 包。
func (l *Loader) Load(name string, doneValidatorRegistered func(key string) bool) (*Card, error) {
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

	if err := l.validateCognitiveMap(&card); err != nil {
		return nil, err
	}
	if err := validateDoneValidator(&card, doneValidatorRegistered); err != nil {
		return nil, err
	}

	return &card, nil
}

// validateCognitiveMap 校验 cognitive_map 文件存在且含 ≥ 6 个槽位标题。
//
// 6 槽位用正则 (?m)^##\s+\d+\. 计数（黑客松借鉴 cairn / DGRS 等队伍设计）。
func (l *Loader) validateCognitiveMap(card *Card) error {
	if card.CognitiveMap == "" {
		return nil
	}
	cmPath := filepath.Join(l.root, card.CognitiveMap)
	data, err := os.ReadFile(cmPath)
	if err != nil {
		return fmt.Errorf("cognitive_map %s: %w", cmPath, err)
	}
	if got := countCognitiveSlots(data); got < cognitiveMapMinSlots {
		return fmt.Errorf(
			"cognitive_map %s 槽位不足：需要 ≥ %d 个 '## N.' 标题，实际 %d 个",
			cmPath, cognitiveMapMinSlots, got,
		)
	}
	return nil
}

// validateDoneValidator 检查 done_validator key 已在 registry 注册。
func validateDoneValidator(card *Card, registered func(key string) bool) error {
	if card.DoneValidator == "" {
		return nil
	}
	if registered == nil {
		return errors.New("done_validator 校验回调未注入")
	}
	if !registered(card.DoneValidator) {
		return fmt.Errorf(
			"done_validator %q 未在 ActionRegistry 注册，请检查 Skill 是否启动前 Register",
			card.DoneValidator,
		)
	}
	return nil
}

// 6 槽位是黑客松共识：感知 / 假设 / 证据 / 推理 / 验证 / 收尾。
const cognitiveMapMinSlots = 6

var cognitiveSlotRe = regexp.MustCompile(`(?m)^##\s+\d+\.`)

func countCognitiveSlots(data []byte) int {
	return len(cognitiveSlotRe.FindAll(data, -1))
}

var (
	frontmatterDelim = []byte("---")
	newline          = []byte("\n")
)

// splitFrontmatter 把 SKILL.md 切成 (frontmatter, body)。
//
// 协议：文件开头（允许前置空白/换行）必须是 ---\n，然后 yaml，然后 \n--- 结束。
// 这是 Jekyll/Hugo 等通用约定，避开正则的复杂度。
func splitFrontmatter(raw []byte) ([]byte, []byte, error) {
	r := bytes.TrimLeft(raw, "\n\r\t ")
	if !bytes.HasPrefix(r, frontmatterDelim) {
		return nil, nil, errors.New("missing frontmatter: 文件开头未发现 '---' 分隔")
	}
	r = r[len(frontmatterDelim):]

	// 找下一个 \n--- 作为闭合
	closeMark := append(append([]byte{}, newline...), frontmatterDelim...)
	idx := bytes.Index(r, closeMark)
	if idx < 0 {
		return nil, nil, errors.New("frontmatter not closed: 未发现闭合的 '\\n---'")
	}
	front := r[:idx]
	body := r[idx+len(closeMark):]
	return front, bytes.TrimLeft(body, "\n\r"), nil
}
