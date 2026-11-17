// Package repro 承载复现配方的信封契约 {kind, recipe, assert} 与配方形状注册表。
//
// kind 说的是配方长什么样、怎样复现，不是业务领域：图、执行者、评估者只认信封三字段，
// recipe 与 assert 的形状、矫正与校验归各形状（Kind）。steps 是内置形状——自然语言步骤
// 加判据描述，没有机器复现通道，由评估者按步骤自主取证；有机器复现通道的形状（如 http）
// 在装配时注册。
//
// 配方只在执行者落图时归一化一次（Registry.Normalize），节点存的就是归一后的信封。
package repro

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Envelope 是复现配方的信封。
type Envelope struct {
	Kind   string          `json:"kind"`
	Recipe json.RawMessage `json:"recipe"`
	Assert json.RawMessage `json:"assert"`
}

// Kind 是一种配方形状：定义 recipe 与 assert 的形状，以及怎样把 LLM 写的配方矫正成
// 这个形状。fields 是配方的全部顶层字段——除信封三字段外，也包括 LLM 笔误写在顶层的
// 本形状字段，由形状自己认领。
type Kind interface {
	// Name 是信封里的 kind 取值。
	Name() string
	// Recognizes 判断漏写 kind 的配方是否属于本形状（按本形状特有字段嗅探）。
	Recognizes(fields map[string]json.RawMessage) bool
	// Normalize 把配方矫正成本形状的 recipe 与 assert；形状不对时返回可行动的报错。
	Normalize(fields map[string]json.RawMessage) (recipe, assert json.RawMessage, err error)
	// Example 是本形状配方的正确写法（漏写 kind 时随报错教给 LLM）。
	Example() string
}

// Registry 是可用的配方形状集合：steps 内置，其余在装配时注册。嗅探按注册顺序，
// steps 最后。
type Registry struct {
	kinds []Kind
}

// NewRegistry 创建配方形状注册表（steps 总是可用）。
func NewRegistry(kinds ...Kind) *Registry {
	return &Registry{kinds: append(append([]Kind{}, kinds...), Steps{})}
}

// Normalize 把 LLM 提交的配方归一化成信封并按形状校验。容忍常见笔误：配方写成步骤
// 列表、assert 误放进 recipe、漏写 kind（按形状嗅探）。返回（信封，教学提示（可空），
// 错误）；教学提示随工具输出回给 LLM。
func (r *Registry) Normalize(raw json.RawMessage) (json.RawMessage, string, error) {
	raw = convertStepList(raw)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, "", fmt.Errorf("repro 解析失败: %w", err)
	}
	note := promoteAssertFromRecipe(fields)

	var name string
	if v, ok := fields["kind"]; ok {
		_ = json.Unmarshal(v, &name)
	}
	kind := r.lookup(name)
	if name == "" {
		kind = r.sniff(fields)
	}
	switch {
	case kind == nil && name == "":
		return nil, "", fmt.Errorf("repro 缺少 kind。\n\n%s", r.examples())
	case kind == nil:
		return nil, "", fmt.Errorf("未知配方形状 %q（已支持: %s）", name, strings.Join(r.names(), ", "))
	}

	recipe, assert, err := kind.Normalize(fields)
	if err != nil {
		return nil, "", err
	}
	out, err := json.Marshal(Envelope{Kind: kind.Name(), Recipe: recipe, Assert: assert})
	if err != nil {
		return nil, "", fmt.Errorf("repro 归一化失败: %w", err)
	}
	return out, note, nil
}

func (r *Registry) lookup(name string) Kind {
	for _, k := range r.kinds {
		if k.Name() == name {
			return k
		}
	}
	return nil
}

func (r *Registry) sniff(fields map[string]json.RawMessage) Kind {
	for _, k := range r.kinds {
		if k.Recognizes(fields) {
			return k
		}
	}
	return nil
}

func (r *Registry) names() []string {
	out := make([]string, 0, len(r.kinds))
	for _, k := range r.kinds {
		out = append(out, k.Name())
	}
	return out
}

func (r *Registry) examples() string {
	var b strings.Builder
	for _, k := range r.kinds {
		fmt.Fprintf(&b, "%s：%s\n", k.Name(), k.Example())
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
	return "\n\nℹ️ 注意：assert 应写在 repro 顶层（与 kind/recipe 平级），本次已自动从 recipe 中提升——下次请直接写对位置。"
}

// convertStepList 把写成步骤列表的配方（["step1", "step2", ...]）转换成 steps 信封——
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
		"kind":   StepsName,
		"recipe": map[string]string{"steps": strings.Join(steps, "\n")},
		"assert": map[string]string{"description": "按步骤执行后观察到配方声称的现象"},
	})
	if err != nil {
		return raw
	}
	return b
}
