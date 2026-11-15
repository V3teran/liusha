// Package repro 承载复现配方的域信封契约 {domain, recipe, assert} 与领域注册表。
//
// 信封是领域中立的：图、执行者、评估者只认信封三字段，recipe 与 assert 的形状、
// 矫正与校验归各领域（Domain）。generic 是内置的中立领域——没有机器复现通道，由
// 评估者按步骤自主取证；其它领域（如 Web）在装配时注册。
//
// 配方只在执行者落图时归一化一次（Registry.Normalize），节点存的就是归一后的信封。
package repro

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Envelope 是复现配方的域信封。
type Envelope struct {
	Domain string          `json:"domain"`
	Recipe json.RawMessage `json:"recipe"`
	Assert json.RawMessage `json:"assert"`
}

// Domain 是一个复现领域：定义 recipe 与 assert 的形状，以及怎样把 LLM 写的配方矫正成
// 这个形状。fields 是配方的全部顶层字段——除信封三字段外，也包括 LLM 笔误写在顶层的
// 本领域字段，由领域自己认领。
type Domain interface {
	// Name 是信封里的 domain 取值。
	Name() string
	// Recognizes 判断漏写 domain 的配方是否属于本领域（按本领域特有字段嗅探）。
	Recognizes(fields map[string]json.RawMessage) bool
	// Normalize 把配方矫正成本领域的 recipe 与 assert；形状不对时返回可行动的报错。
	Normalize(fields map[string]json.RawMessage) (recipe, assert json.RawMessage, err error)
	// Example 是本领域配方的正确写法（漏写 domain 时随报错教给 LLM）。
	Example() string
}

// Registry 是可用的复现领域集合：generic 内置，其余在装配时注册。嗅探按注册顺序，
// generic 最后。
type Registry struct {
	domains []Domain
}

// NewRegistry 创建领域注册表（generic 总是可用）。
func NewRegistry(domains ...Domain) *Registry {
	return &Registry{domains: append(append([]Domain{}, domains...), Generic{})}
}

// Normalize 把 LLM 提交的配方归一化成域信封并按领域校验。容忍常见笔误：配方写成步骤
// 列表、assert 误放进 recipe、漏写 domain（按领域嗅探）。返回（信封，教学提示（可空），
// 错误）；教学提示随工具输出回给 LLM。
func (r *Registry) Normalize(raw json.RawMessage) (json.RawMessage, string, error) {
	raw = convertStepList(raw)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, "", fmt.Errorf("repro 解析失败: %w", err)
	}
	note := promoteAssertFromRecipe(fields)

	var name string
	if v, ok := fields["domain"]; ok {
		_ = json.Unmarshal(v, &name)
	}
	domain := r.lookup(name)
	if name == "" {
		domain = r.sniff(fields)
	}
	switch {
	case domain == nil && name == "":
		return nil, "", fmt.Errorf("repro 缺少 domain 信封。\n\n%s", r.examples())
	case domain == nil:
		return nil, "", fmt.Errorf("未知复现域 %q（已支持: %s）", name, strings.Join(r.names(), ", "))
	}

	recipe, assert, err := domain.Normalize(fields)
	if err != nil {
		return nil, "", err
	}
	out, err := json.Marshal(Envelope{Domain: domain.Name(), Recipe: recipe, Assert: assert})
	if err != nil {
		return nil, "", fmt.Errorf("repro 归一化失败: %w", err)
	}
	return out, note, nil
}

func (r *Registry) lookup(name string) Domain {
	for _, d := range r.domains {
		if d.Name() == name {
			return d
		}
	}
	return nil
}

func (r *Registry) sniff(fields map[string]json.RawMessage) Domain {
	for _, d := range r.domains {
		if d.Recognizes(fields) {
			return d
		}
	}
	return nil
}

func (r *Registry) names() []string {
	out := make([]string, 0, len(r.domains))
	for _, d := range r.domains {
		out = append(out, d.Name())
	}
	return out
}

func (r *Registry) examples() string {
	var b strings.Builder
	for _, d := range r.domains {
		fmt.Fprintf(&b, "%s 域：%s\n", d.Name(), d.Example())
	}
	return strings.TrimRight(b.String(), "\n")
}

// promoteAssertFromRecipe 处理 assert 误放进 recipe 的常见笔误：提升到顶层，返回教学
// 提示让 LLM 下次写对位置。
func promoteAssertFromRecipe(fields map[string]json.RawMessage) string {
	if len(fields["assert"]) > 0 || len(fields["recipe"]) == 0 {
		return ""
	}
	var recipe map[string]json.RawMessage
	if json.Unmarshal(fields["recipe"], &recipe) != nil || len(recipe["assert"]) == 0 {
		return ""
	}
	fields["assert"] = recipe["assert"]
	delete(recipe, "assert")
	if b, err := json.Marshal(recipe); err == nil {
		fields["recipe"] = b
	}
	return "\n\nℹ️ 注意：assert 应写在 repro 顶层（与 domain/recipe 平级），本次已自动从 recipe 中提升——下次请直接写对位置。"
}

// convertStepList 把写成步骤列表的配方（["step1", "step2", ...]）转换成 generic 信封——
// LLM 偶尔把复现步骤写成字符串列表。数组内非字符串元素时原样返回（由解析报错）。
func convertStepList(raw json.RawMessage) json.RawMessage {
	if !strings.HasPrefix(strings.TrimSpace(string(raw)), "[") {
		return raw
	}
	var steps []string
	if json.Unmarshal(raw, &steps) != nil || len(steps) == 0 {
		return raw
	}
	b, err := json.Marshal(map[string]any{
		"domain": GenericName,
		"recipe": map[string]string{"steps": strings.Join(steps, "\n")},
		"assert": map[string]string{"description": "按步骤执行后观察到配方声称的现象"},
	})
	if err != nil {
		return raw
	}
	return b
}
