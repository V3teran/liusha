package configstore

import (
	"bytes"
	"fmt"

	"github.com/V3teran/liusha/internal/override"
	"github.com/V3teran/liusha/internal/skillstore"
)

// 技能与场景同一模型（一张表、内置为出厂种子）：内容的合并、恢复出厂与相对出厂的状态
// 都按「可修改字段表」逐字段处理，这里是两者共用的实现。

// fieldEnabled 是启停字段：它是状态而非内容，不参与「改没改过」的哈希，也没有「出厂已更新」一说。
const fieldEnabled = "enabled"

// mergeFields 把提交的字段合入当前内容（只接受 schema 里的字段）。
func mergeFields[T any](schema override.Schema, cur T, changes override.Document) (T, error) {
	var zero T
	doc, err := override.ToDocument(cur)
	if err != nil {
		return zero, err
	}
	for name, v := range changes {
		if _, ok := schema[name]; !ok {
			return zero, fmt.Errorf("%w：字段 %q 不可修改", override.ErrInvalid, name)
		}
		doc[name] = v
	}
	var next T
	if err := override.FromDocument(doc, &next); err != nil {
		return zero, fmt.Errorf("%w：%w", override.ErrInvalid, err)
	}
	return next, nil
}

// resetFields 把指定字段恢复为出厂值（不传字段则全部恢复）。
func resetFields[T any](schema override.Schema, cur, def T, fields []string) (T, error) {
	var zero T
	defDoc, err := override.ToDocument(def)
	if err != nil {
		return zero, err
	}
	if len(fields) == 0 {
		return def, nil
	}
	changes := override.Document{}
	for _, f := range fields {
		if _, ok := schema[f]; !ok {
			return zero, fmt.Errorf("%w：字段 %q 不可自定义", override.ErrInvalid, f)
		}
		changes[f] = defDoc[f]
	}
	return mergeFields(schema, cur, changes)
}

// describeAgainstSeed 逐字段比较当前内容与出厂内容，给出不同于出厂的字段的状态。
// 内容字段的「出厂已更新」= 基线已知且不是当前出厂（用户改过之后出厂又发了新版）；基线
// 只记哈希，不记当年的原文，因此 Baseline 只在基线就是当前出厂时给出。启停是状态，没有
// 出厂更新一说。
func describeAgainstSeed[T any](cur, def T, seedHash *string, currentSeedHash string) (map[string]override.State, error) {
	out := map[string]override.State{}
	curDoc, err := override.ToDocument(cur)
	if err != nil {
		return nil, err
	}
	defDoc, err := override.ToDocument(def)
	if err != nil {
		return nil, err
	}
	upstreamChanged := seedHash != nil && *seedHash != currentSeedHash
	for name, d := range defDoc {
		if bytes.Equal(curDoc[name], d) {
			continue
		}
		st := override.State{Kind: override.Replace.String(), Default: d}
		switch {
		case name == fieldEnabled:
			st.Baseline = d
		case upstreamChanged:
			st.DefaultChanged = true
		case seedHash != nil:
			st.Baseline = d
		}
		out[name] = st
	}
	return out, nil
}

// mergeContent 把提交的字段合入技能内容并校验。
func mergeContent(cur skillstore.Content, changes override.Document) (skillstore.Content, error) {
	next, err := mergeFields(skillstore.Schema(), cur, changes)
	if err != nil {
		return skillstore.Content{}, err
	}
	return next, skillstore.Validate(next)
}

// resetContent 把技能的指定字段恢复为出厂内容并校验。
func resetContent(cur, def skillstore.Content, fields []string) (skillstore.Content, error) {
	next, err := resetFields(skillstore.Schema(), cur, def, fields)
	if err != nil {
		return skillstore.Content{}, err
	}
	return next, skillstore.Validate(next)
}
