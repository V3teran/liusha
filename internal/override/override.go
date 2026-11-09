// Package override 是「出厂默认 + 用户自定义」分层的字段级引擎。
//
// 一个可自定义资源（agent、内置技能、一组系统设置）被看作一份文档：字段名 → JSON 值。
// 出厂文档来自代码，用户层只记录被自定义的字段。字段按语义分两种：
//
//   - Replace（替换）：文本、数值、枚举、布尔。记录用户的值与基线——写入时
//     依据的出厂值原文，据此判断出厂是否已更新、展示三方对比。
//   - Set（集合）：名单。记录用户相对出厂集合的新增与删除，生效值 = 出厂 ∪ 新增 − 删除，
//     产品新增的出厂项自动生效，用户删掉的项持续删除。
//
// 规则：
//   - 保存时与出厂值相同的字段不算自定义（不留看不见的「钉住」状态）；
//   - 集合以操作提交（勾选 on / 取消 off），只记录本次实际改变的项，早先的增删
//     意图不因再次保存而丢失；
//   - 通配 "*"（全部，含以后新增）：「全部」状态下取消某项按全集展开再减去，
//     以后新增的项仍自动生效；取消 "*" 本身表示退出「全部」。
//
// 本包是纯函数，不触碰存储；持久化与并发控制由各资源的 store 负责。
package override

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
)

// ErrInvalid 标记用户提交的配置不合法（未知字段、引用不存在的元素、校验不通过）。
// 各资源的校验错误都包装它，接口层据此统一映射为 400。
var ErrInvalid = errors.New("提交的配置不合法")

// Kind 是字段的自定义语义。
type Kind int

const (
	// Replace 整值替换：文本、数值、枚举、布尔。
	Replace Kind = iota
	// Set 集合增删：名单类字段。
	Set
)

func (k Kind) String() string {
	if k == Set {
		return "set"
	}
	return "replace"
}

// Wildcard 是集合字段的通配元素：「全部，含以后新增」。
const Wildcard = "*"

var wildcardJSON = json.RawMessage(`"*"`)

// Field 描述一个可自定义字段。
type Field struct {
	Kind Kind
	// Universe 返回集合字段的元素全集，用于展开通配与校验引用；nil 表示该字段
	// 不支持通配、元素不受全集约束（如代理过滤名单里的任意主机名）。
	Universe func() []json.RawMessage
}

// Schema 是资源的可自定义字段表（字段名 → 描述）。
type Schema map[string]Field

// Document 是一份资源文档：字段名 → JSON 值（出厂值或生效值）。
type Document map[string]json.RawMessage

// Entry 是一个字段的自定义记录。Replace 用 Value/Baseline，Set 用 Added/Removed。
type Entry struct {
	Value json.RawMessage `json:"value,omitempty"`
	// Baseline 是写入时依据的出厂值原文；缺省表示基线未知（升级前的修改）。
	Baseline json.RawMessage   `json:"baseline,omitempty"`
	Added    []json.RawMessage `json:"added,omitempty"`
	Removed  []json.RawMessage `json:"removed,omitempty"`
}

// Overrides 是一个资源的用户层：字段名 → 自定义记录。只含被自定义的字段。
type Overrides map[string]Entry

// SetChange 是集合字段的一次提交：勾选与取消的元素（可含通配 "*"）。
// 以操作而非最终清单提交，才能区分「全部中取消几项」与「只要这几项」两种意图。
type SetChange struct {
	On  []json.RawMessage `json:"on"`
	Off []json.RawMessage `json:"off"`
}

// State 描述一个已自定义字段，供接口展示「已自定义 / 出厂已更新 / 已失效」。
type State struct {
	Kind    string          `json:"kind"`
	Default json.RawMessage `json:"default"`
	// Replace：
	Baseline       json.RawMessage `json:"baseline"`        // null = 升级前的修改，基线未知
	DefaultChanged bool            `json:"default_changed"` // 基线已知且与当前出厂值不同
	MatchesDefault bool            `json:"matches_default"` // 用户的值恰与当前出厂值相同
	// Set：
	Added   []json.RawMessage `json:"added,omitempty"`
	Removed []json.RawMessage `json:"removed,omitempty"`
	Stale   []json.RawMessage `json:"stale,omitempty"` // 新增项里已不在全集中的（已下线）
}

// Apply 合成生效文档：出厂文档逐字段叠加自定义。
func Apply(schema Schema, defaults Document, ov Overrides) Document {
	out := make(Document, len(defaults))
	for name, def := range defaults {
		out[name] = def
	}
	for name, e := range ov {
		f, ok := schema[name]
		if !ok {
			continue // 出厂已移除的字段：残留自定义不生效
		}
		if f.Kind == Set {
			out[name] = mustMarshal(effectiveSet(f, decodeList(defaults[name]), e))
			continue
		}
		if len(e.Value) > 0 {
			out[name] = e.Value
		}
	}
	return out
}

// Patch 把用户提交的字段写入自定义层并返回新的用户层（不修改入参）。
// Replace 字段提交新值，与出厂值相同即清除自定义；Set 字段提交 SetChange。
// 未知字段、Set 字段引用全集外的元素、同一元素既勾选又取消，均报错。
func Patch(schema Schema, defaults Document, ov Overrides, changes Document) (Overrides, error) {
	out := clone(ov)
	for name, value := range changes {
		f, ok := schema[name]
		if !ok {
			return nil, fmt.Errorf("%w：字段 %q 不可自定义", ErrInvalid, name)
		}
		def := defaults[name]
		if f.Kind == Replace {
			if equalJSON(value, def) {
				delete(out, name)
				continue
			}
			out[name] = Entry{Value: value, Baseline: def}
			continue
		}
		var change SetChange
		if err := json.Unmarshal(value, &change); err != nil {
			return nil, fmt.Errorf("%w：字段 %q 应为 {on, off}: %w", ErrInvalid, name, err)
		}
		e, err := applyChange(f, decodeList(def), out[name], change)
		if err != nil {
			return nil, fmt.Errorf("%w：字段 %q: %w", ErrInvalid, name, err)
		}
		setEntry(out, name, e)
	}
	return out, nil
}

// Toggle 对集合字段单个元素做勾选（on=true）或取消，返回新的用户层。
func Toggle(schema Schema, defaults Document, ov Overrides, name string, elem json.RawMessage, on bool) (Overrides, error) {
	f, ok := schema[name]
	if !ok || f.Kind != Set {
		return nil, fmt.Errorf("%w：字段 %q 不是可自定义的集合", ErrInvalid, name)
	}
	change := SetChange{Off: []json.RawMessage{elem}}
	if on {
		change = SetChange{On: []json.RawMessage{elem}}
	}
	out := clone(ov)
	e, err := applyChange(f, decodeList(defaults[name]), out[name], change)
	if err != nil {
		return nil, fmt.Errorf("%w：字段 %q: %w", ErrInvalid, name, err)
	}
	setEntry(out, name, e)
	return out, nil
}

// Reset 清除指定字段的自定义（不传字段则全部清除），返回新的用户层。
func Reset(ov Overrides, fields ...string) Overrides {
	if len(fields) == 0 {
		return Overrides{}
	}
	out := clone(ov)
	for _, name := range fields {
		delete(out, name)
	}
	return out
}

// Describe 生成每个已自定义字段的状态。出厂已移除的字段不出现。
func Describe(schema Schema, defaults Document, ov Overrides) map[string]State {
	out := make(map[string]State, len(ov))
	for name, e := range ov {
		f, ok := schema[name]
		if !ok {
			continue
		}
		def := defaults[name]
		s := State{Kind: f.Kind.String(), Default: def}
		if f.Kind == Replace {
			s.Baseline = e.Baseline
			s.DefaultChanged = len(e.Baseline) > 0 && !equalJSON(e.Baseline, def)
			s.MatchesDefault = equalJSON(e.Value, def)
		} else {
			s.Added = orEmpty(e.Added)
			s.Removed = orEmpty(e.Removed)
			if f.Universe != nil {
				universe := f.Universe()
				for _, a := range e.Added {
					if !isWildcard(a) && !containsJSON(universe, a) {
						s.Stale = append(s.Stale, a)
					}
				}
			}
		}
		out[name] = s
	}
	return out
}

// RemoveMember 从某集合字段的新增、删除记录里抹去一个元素（被引用对象已删除时
// 清理用户层的悬空引用），返回新的用户层与是否有改动。
func RemoveMember(ov Overrides, name string, elem json.RawMessage) (Overrides, bool) {
	e, ok := ov[name]
	if !ok {
		return ov, false
	}
	added, removed := withoutJSON(e.Added, elem), withoutJSON(e.Removed, elem)
	if len(added) == len(e.Added) && len(removed) == len(e.Removed) {
		return ov, false
	}
	out := clone(ov)
	e.Added, e.Removed = added, removed
	setEntry(out, name, e)
	return out, true
}

// ── 集合语义 ───────────────────────────────────────────────────────────

func effectiveSet(f Field, def []json.RawMessage, e Entry) []json.RawMessage {
	base := unionJSON(def, e.Added)
	if containsJSON(base, wildcardJSON) && !containsJSON(e.Removed, wildcardJSON) {
		if len(e.Removed) == 0 || f.Universe == nil {
			return []json.RawMessage{wildcardJSON}
		}
		return minusJSON(f.Universe(), e.Removed)
	}
	return minusJSON(withoutJSON(base, wildcardJSON), e.Removed)
}

// member 报告元素是否在生效集合中（通配生效时全集外的元素也视为在内）。
func member(f Field, def []json.RawMessage, e Entry, elem json.RawMessage) bool {
	eff := effectiveSet(f, def, e)
	if len(eff) == 1 && isWildcard(eff[0]) {
		return !containsJSON(e.Removed, elem)
	}
	return containsJSON(eff, elem)
}

func toggleOn(f Field, def []json.RawMessage, e Entry, elem json.RawMessage) Entry {
	e.Removed = withoutJSON(e.Removed, elem)
	if !member(f, def, e, elem) {
		e.Added = append(slices.Clone(e.Added), elem)
	}
	return e
}

func toggleOff(f Field, def []json.RawMessage, e Entry, elem json.RawMessage) Entry {
	e.Added = withoutJSON(e.Added, elem)
	if member(f, def, e, elem) {
		e.Removed = append(slices.Clone(e.Removed), elem)
	}
	return e
}

// applyChange 依次执行一次提交：先处理通配（进出「全部」状态决定其余元素的含义），
// 再取消、再勾选。
func applyChange(f Field, def []json.RawMessage, e Entry, c SetChange) (Entry, error) {
	if err := checkMembers(f, append(slices.Clone(c.On), c.Off...)); err != nil {
		return e, err
	}
	for _, x := range c.On {
		if containsJSON(c.Off, x) {
			return e, fmt.Errorf("%s 不能同时勾选和取消", string(x))
		}
	}
	steps := []struct {
		elems []json.RawMessage
		on    bool
	}{
		{filterJSON(c.Off, isWildcard), false},
		{filterJSON(c.On, isWildcard), true},
		{rejectJSON(c.Off, isWildcard), false},
		{rejectJSON(c.On, isWildcard), true},
	}
	for _, st := range steps {
		for _, x := range st.elems {
			if st.on {
				e = toggleOn(f, def, e, x)
			} else {
				e = toggleOff(f, def, e, x)
			}
		}
	}
	return e, nil
}

func checkMembers(f Field, elems []json.RawMessage) error {
	if f.Universe == nil {
		return nil
	}
	universe := f.Universe()
	for _, x := range elems {
		if !isWildcard(x) && !containsJSON(universe, x) {
			return fmt.Errorf("%s 不存在", string(x))
		}
	}
	return nil
}

func setEntry(ov Overrides, name string, e Entry) {
	if len(e.Value) == 0 && len(e.Added) == 0 && len(e.Removed) == 0 {
		delete(ov, name)
		return
	}
	ov[name] = e
}

// ── JSON 工具 ──────────────────────────────────────────────────────────

// canon 把 JSON 值规整为可比较的规范串（解码再编码：消除空白、数字书写差异）。
func canon(raw json.RawMessage) string {
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return string(raw)
	}
	if n, ok := v.(json.Number); ok {
		if f, err := n.Float64(); err == nil {
			return fmt.Sprintf("n:%g", f)
		}
	}
	b, err := json.Marshal(v)
	if err != nil {
		return string(raw)
	}
	return string(b)
}

func equalJSON(a, b json.RawMessage) bool { return canon(a) == canon(b) }

func isWildcard(raw json.RawMessage) bool { return canon(raw) == canon(wildcardJSON) }

func containsJSON(list []json.RawMessage, x json.RawMessage) bool {
	k := canon(x)
	for _, v := range list {
		if canon(v) == k {
			return true
		}
	}
	return false
}

func withoutJSON(list []json.RawMessage, x json.RawMessage) []json.RawMessage {
	k := canon(x)
	var out []json.RawMessage
	for _, v := range list {
		if canon(v) != k {
			out = append(out, v)
		}
	}
	return out
}

func unionJSON(a, b []json.RawMessage) []json.RawMessage {
	out := make([]json.RawMessage, 0, len(a)+len(b))
	for _, v := range append(slices.Clone(a), b...) {
		if !containsJSON(out, v) {
			out = append(out, v)
		}
	}
	return out
}

func minusJSON(a, b []json.RawMessage) []json.RawMessage {
	out := make([]json.RawMessage, 0, len(a))
	for _, v := range a {
		if !containsJSON(b, v) && !containsJSON(out, v) {
			out = append(out, v)
		}
	}
	return out
}

func decodeList(raw json.RawMessage) []json.RawMessage {
	var list []json.RawMessage
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil
	}
	return list
}

func filterJSON(list []json.RawMessage, keep func(json.RawMessage) bool) []json.RawMessage {
	var out []json.RawMessage
	for _, v := range list {
		if keep(v) {
			out = append(out, v)
		}
	}
	return out
}

func rejectJSON(list []json.RawMessage, drop func(json.RawMessage) bool) []json.RawMessage {
	return filterJSON(list, func(v json.RawMessage) bool { return !drop(v) })
}

func orEmpty(list []json.RawMessage) []json.RawMessage {
	if list == nil {
		return []json.RawMessage{}
	}
	return list
}

func mustMarshal(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("override: 序列化失败: %v", err))
	}
	return b
}

func clone(ov Overrides) Overrides {
	out := make(Overrides, len(ov))
	for k, v := range ov {
		v.Added = slices.Clone(v.Added)
		v.Removed = slices.Clone(v.Removed)
		out[k] = v
	}
	return out
}

// ToDocument 把带 json 标签的结构体转为文档。
func ToDocument(v any) (Document, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var d Document
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, err
	}
	return d, nil
}

// FromDocument 把文档解码进结构体。
func FromDocument(d Document, v any) error {
	b, err := json.Marshal(d)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// Strings 把字符串切片转为集合元素（构造 Universe 用）。
func Strings(names []string) []json.RawMessage {
	out := make([]json.RawMessage, 0, len(names))
	for _, n := range names {
		out = append(out, mustMarshal(n))
	}
	return out
}

// SchemaOf 按结构体字段类型推导字段表：切片字段为集合，其余为整值替换；
// 字段名取 json 标签。用于所有字段均可自定义、且集合不受全集约束的资源。
func SchemaOf(v any) Schema {
	t := reflect.TypeOf(v)
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	out := Schema{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}
		kind := Replace
		if f.Type.Kind() == reflect.Slice {
			kind = Set
		}
		out[name] = Field{Kind: kind}
	}
	return out
}
