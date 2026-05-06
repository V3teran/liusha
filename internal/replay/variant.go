package replay

import (
	"encoding/json"
	"fmt"
	"net/url"
)

// BaselineVariantName 是默认的"原样重放"变体名。
// BAC 场景下所有 replay 都是这个 variant；SQLi 等场景在它基础上新增 err_quote / bool_true / time_sleep 等。
const BaselineVariantName = "baseline"

// Mutation 类型枚举。
//   - passthrough：不变形原始请求（用于 BAC 单纯多身份 / SQLi 的 baseline 对照）
//   - param_inject：把 payload 注入到 query / body_json / body_form 的指定字段
const (
	MutationPassthrough = "passthrough"
	MutationParamInject = "param_inject"
)

// param_inject 的注入位置（Mutation.Where 字段值）。
const (
	WhereQuery    = "query"
	WhereBodyJSON = "body_json"
	WhereBodyForm = "body_form"
)

// Mutation.Mode 取值。
const (
	ModeReplace = "replace" // 默认：用 Value 完全替换原值
	ModeAppend  = "append"  // 在原值后追加 Value（payload 注入常用：1' OR '1'='1 拼到 id=7 后 → id=7' OR '1'='1）
)

// Variant 描述一个请求变体：身份替换之外的"加工"步骤。
// Name 用于在响应矩阵里标记是哪个变体，Mutation 描述如何变形原始请求。
type Variant struct {
	Name     string   `json:"name"`
	Mutation Mutation `json:"mutation"`
}

// Mutation 描述如何把原始请求变形成一个新请求。
//
// 字段语义：
//   - Type：变换类型（passthrough / param_inject / header_inject / body_inject 等）
//   - Where：注入位置（query / path_param / header / body_form / body_json）
//   - Field：参数 / 字段 / 头名
//   - Value：替换值（payload）
//   - Mode：replace（默认） / append
//
// passthrough 类型忽略所有其他字段，直接返回原始请求副本。
type Mutation struct {
	Type  string `json:"type"`
	Where string `json:"where,omitempty"`
	Field string `json:"field,omitempty"`
	Value string `json:"value,omitempty"`
	Mode  string `json:"mode,omitempty"`
}

// DefaultBaselineVariant 返回单一的 passthrough variant，
// 用于 caller 不传 variants 列表时的兜底（仅做身份替换，不变形请求）。
func DefaultBaselineVariant() Variant {
	return Variant{
		Name:     BaselineVariantName,
		Mutation: Mutation{Type: MutationPassthrough},
	}
}

// ApplyMutation 把 mutation 应用到 raw 上，返回深拷贝后的新 RawRequest。
// 不修改入参 raw（headers / body / URL 全走拷贝）。
//
// 已支持类型：
//   - passthrough：无变形（BAC 多身份重放、SQLi baseline 对照都走这条）
//   - param_inject：把 Value 注入到 query / body_json / body_form 的 Field 字段
//
// 暂不支持的类型（path_param / header_inject / body_xml）返回 ErrUnsupportedMutation。
func ApplyMutation(raw RawRequest, m Mutation) (RawRequest, error) {
	switch m.Type {
	case "", MutationPassthrough:
		return cloneRaw(raw), nil
	case MutationParamInject:
		return applyParamInject(cloneRaw(raw), m)
	default:
		return RawRequest{}, fmt.Errorf("%w: %q", ErrUnsupportedMutation, m.Type)
	}
}

// applyParamInject 按 m.Where 分发到具体注入函数；调用方应已传入深拷贝的 raw。
func applyParamInject(raw RawRequest, m Mutation) (RawRequest, error) {
	if m.Field == "" {
		return RawRequest{}, fmt.Errorf("param_inject: field 必填")
	}
	switch m.Where {
	case "", WhereQuery:
		return injectQuery(raw, m)
	case WhereBodyJSON:
		return injectBodyJSON(raw, m)
	case WhereBodyForm:
		return injectBodyForm(raw, m)
	default:
		return RawRequest{}, fmt.Errorf("%w: where=%q", ErrUnsupportedMutation, m.Where)
	}
}

// injectQuery 把 m.Value 注入到 URL 的 query 字符串中 m.Field 字段。
//
// Mode=replace：用 Value 替换字段当前值（若字段不存在则新建）
// Mode=append：在原值后追加 Value（payload 注入常用：原值 7 + 追加 ' OR '1'='1 → 7' OR '1'='1）
func injectQuery(raw RawRequest, m Mutation) (RawRequest, error) {
	u, err := url.Parse(raw.URL)
	if err != nil {
		return RawRequest{}, fmt.Errorf("param_inject query: parse url %q: %w", raw.URL, err)
	}
	q := u.Query()
	cur := q.Get(m.Field)
	switch m.Mode {
	case "", ModeReplace:
		q.Set(m.Field, m.Value)
	case ModeAppend:
		q.Set(m.Field, cur+m.Value)
	default:
		return RawRequest{}, fmt.Errorf("param_inject query: 未知 mode=%q", m.Mode)
	}
	u.RawQuery = q.Encode()
	raw.URL = u.String()
	return raw, nil
}

// injectBodyJSON 把 m.Value 注入到 JSON body 的顶层 m.Field 字段。
// 嵌套字段（"user.name"）暂不支持，需要时再扩 dotted-path 解析。
func injectBodyJSON(raw RawRequest, m Mutation) (RawRequest, error) {
	if len(raw.Body) == 0 {
		raw.Body = []byte(`{}`)
	}
	var obj map[string]any
	if err := json.Unmarshal(raw.Body, &obj); err != nil {
		return RawRequest{}, fmt.Errorf("param_inject body_json: 解析 body 失败: %w", err)
	}
	switch m.Mode {
	case "", ModeReplace:
		obj[m.Field] = m.Value
	case ModeAppend:
		cur, _ := obj[m.Field].(string)
		obj[m.Field] = cur + m.Value
	default:
		return RawRequest{}, fmt.Errorf("param_inject body_json: 未知 mode=%q", m.Mode)
	}
	encoded, err := json.Marshal(obj)
	if err != nil {
		return RawRequest{}, fmt.Errorf("param_inject body_json: 重新序列化失败: %w", err)
	}
	raw.Body = encoded
	return raw, nil
}

// injectBodyForm 把 m.Value 注入到 application/x-www-form-urlencoded body 的 m.Field 字段。
func injectBodyForm(raw RawRequest, m Mutation) (RawRequest, error) {
	values, err := url.ParseQuery(string(raw.Body))
	if err != nil {
		return RawRequest{}, fmt.Errorf("param_inject body_form: 解析 body 失败: %w", err)
	}
	cur := values.Get(m.Field)
	switch m.Mode {
	case "", ModeReplace:
		values.Set(m.Field, m.Value)
	case ModeAppend:
		values.Set(m.Field, cur+m.Value)
	default:
		return RawRequest{}, fmt.Errorf("param_inject body_form: 未知 mode=%q", m.Mode)
	}
	raw.Body = []byte(values.Encode())
	return raw, nil
}

// ErrUnsupportedMutation 标记 ApplyMutation 还不支持的 mutation 类型；
// 1d 之后的 Step 2 / Step 3 会按需扩展 case 分支。
var ErrUnsupportedMutation = fmt.Errorf("unsupported mutation type")

// cloneRaw 深拷贝 raw：headers map、body slice 都新分配；URL/Method 是值类型直接复制。
func cloneRaw(raw RawRequest) RawRequest {
	out := RawRequest{
		Method: raw.Method,
		URL:    raw.URL,
		Body:   append([]byte(nil), raw.Body...),
	}
	if raw.Headers != nil {
		out.Headers = raw.Headers.Clone()
	}
	return out
}
