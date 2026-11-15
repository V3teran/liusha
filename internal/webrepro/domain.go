// Package webrepro 是 Web 复现领域：配方是一个完整的 HTTP 请求（可带良性基线），判据
// 是状态码、响应体、响应头与耗时谓词。它向中立核心提供两样东西——配方的形状矫正
// （repro.Domain）与机器复现通道（evaluator.Replayer）。
package webrepro

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Name 是 Web 领域在信封里的 domain 取值。
const Name = "web"

// MinBodyContainsLen 是 body_contains 子串的最小鉴别长度：过短的子串（如 "1"/"2"/"3"）
// 几乎必然命中任意页面。写入侧与复现通道共用此阈值。
const MinBodyContainsLen = 4

// Domain 实现 repro.Domain。
type Domain struct{}

// Name 实现 repro.Domain。
func (Domain) Name() string { return Name }

// Recognizes 实现 repro.Domain：recipe 内或顶层（笔误）写了 request。
func (Domain) Recognizes(fields map[string]json.RawMessage) bool {
	if len(fields["request"]) > 0 {
		return true
	}
	var recipe struct {
		Request json.RawMessage `json:"request"`
	}
	return json.Unmarshal(fields["recipe"], &recipe) == nil && len(recipe.Request) > 0
}

// Example 实现 repro.Domain。
func (Domain) Example() string {
	return `{"domain": "web", "recipe": {"request": {"method": "GET", "url": "http://host/api?id=payload", "headers": {}, "body": ""}}, "assert": {"body_contains": [...]}}`
}

// Normalize 实现 repro.Domain：扁平形状包装 + assert 形状矫正 + request 与鉴别力校验。
func (Domain) Normalize(fields map[string]json.RawMessage) (recipe, assert json.RawMessage, err error) {
	switch {
	case len(fields["recipe"]) > 0:
		recipe = fields["recipe"]
	case len(fields["request"]) > 0:
		// 扁平形状（LLM 常见笔误：request/baseline 写在顶层未嵌入 recipe）→ 提进 recipe。
		wrapped := map[string]json.RawMessage{"request": fields["request"]}
		if len(fields["baseline"]) > 0 {
			wrapped["baseline"] = fields["baseline"]
		}
		if recipe, err = json.Marshal(wrapped); err != nil {
			return nil, nil, fmt.Errorf("repro 归一化失败: %w", err)
		}
	default:
		return nil, nil, fmt.Errorf("web 域 repro 缺少 recipe.request。\n\n正确格式：%s", Domain{}.Example())
	}
	assert = fields["assert"]
	if len(assert) == 0 {
		return nil, nil, fmt.Errorf("repro 缺少 assert 字段。\n\n" +
			"web 域可用字段: status_code, body_contains, body_not_contains, header_contains, header_not_contains, min_duration_ms")
	}
	assert = coerceAssertShape(assert)
	if err := validateRequestRecipe(recipe); err != nil {
		return nil, nil, err
	}
	if err := teachAssertDiscriminative(assert); err != nil {
		return nil, nil, err
	}
	return recipe, assert, nil
}

// coerceAssertShape 对 assert 做宽容形状矫正（LLM 输出形状不可靠，写入侧统一归一到
// 复现通道期望的类型）：
//   - body_contains/body_not_contains 传单个字符串时自动包装成单元素数组；
//   - header_contains/header_not_contains 传数组（对象数组/字符串数组）时合并成 key→value map；
//   - status_code/min_duration_ms 传数字字符串时转成数字。
//
// 矫正不了的形状原样透传（复现通道报错兜底）。
func coerceAssertShape(assert json.RawMessage) json.RawMessage {
	var raw map[string]json.RawMessage
	if json.Unmarshal(assert, &raw) != nil {
		return assert
	}
	changed := false

	for _, field := range []string{"body_contains", "body_not_contains"} {
		v, ok := raw[field]
		if !ok || len(v) == 0 {
			continue
		}
		trimmed := strings.TrimSpace(string(v))
		if strings.HasPrefix(trimmed, `"`) && json.Valid(v) {
			raw[field] = json.RawMessage("[" + trimmed + "]")
			changed = true
		}
	}

	// 响应头谓词数组 → map（复现通道期望 key→value；形状放任会在通道侧解析失败，该观察
	// 永远无法验证）
	for _, field := range []string{"header_contains", "header_not_contains"} {
		v, ok := raw[field]
		if !ok || len(v) == 0 || !strings.HasPrefix(strings.TrimSpace(string(v)), "[") {
			continue
		}
		if m := mergeHeaderArray(v); m != nil {
			if b, err := json.Marshal(m); err == nil {
				raw[field] = b
				changed = true
			}
		}
	}

	for _, field := range []string{"status_code", "min_duration_ms"} {
		v, ok := raw[field]
		if !ok || len(v) == 0 {
			continue
		}
		trimmed := strings.TrimSpace(string(v))
		if len(trimmed) > 1 && trimmed[0] == '"' && trimmed[len(trimmed)-1] == '"' {
			inner := trimmed[1 : len(trimmed)-1]
			if isJSONInt(inner) {
				raw[field] = json.RawMessage(inner)
				changed = true
			}
		}
	}

	if !changed {
		return assert
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return assert
	}
	return b
}

// mergeHeaderArray 把响应头谓词的数组形状合并成 map：
// [{"X-Foo":"a"},{"X-Bar":"b"}] / ["X-Foo: a","X-Bar=b","X-Baz"] → {"x-foo":"a","x-bar":"b","x-baz":""}。
// 只写头名即子串为空（header_contains 要求头存在，header_not_contains 要求头不存在）。
// 键统一小写（与复现通道 HTTP 头匹配口径一致）；无法辨认的元素跳过，一个都拆不出时
// 返回 nil（原样透传，让复现通道报出真实形状）。
func mergeHeaderArray(v json.RawMessage) map[string]string {
	var elems []json.RawMessage
	if json.Unmarshal(v, &elems) != nil {
		return nil
	}
	merged := map[string]string{}
	for _, e := range elems {
		var obj map[string]json.RawMessage
		var str string
		switch {
		case json.Unmarshal(e, &obj) == nil:
			for k, rv := range obj {
				var s string
				if json.Unmarshal(rv, &s) == nil {
					merged[strings.ToLower(strings.TrimSpace(k))] = s
				}
			}
		case json.Unmarshal(e, &str) == nil:
			if k, val, ok := splitHeaderPair(str); ok {
				merged[strings.ToLower(k)] = val
			} else if name := strings.TrimSpace(str); isHeaderName(name) {
				merged[strings.ToLower(name)] = ""
			}
		}
	}
	if len(merged) == 0 {
		return nil
	}
	return merged
}

// splitHeaderPair 拆 "Key: value" / "Key=value" 头对。
func splitHeaderPair(s string) (key, val string, ok bool) {
	for _, sep := range []string{": ", ":", "="} {
		if idx := strings.Index(s, sep); idx > 0 {
			k := strings.TrimSpace(s[:idx])
			v := strings.TrimSpace(s[idx+len(sep):])
			if k != "" && v != "" {
				return k, v, true
			}
		}
	}
	return "", "", false
}

// isHeaderName 判断字符串是否是合法的 HTTP 头名（RFC 9110 token）。
func isHeaderName(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0:
		default:
			return false
		}
	}
	return true
}

// isJSONInt 判断字符串是否为纯整数形态（status_code/min_duration_ms 用）。
func isJSONInt(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// validateRequestRecipe 校验 recipe：request 四字段齐全 + URL 绝对。
func validateRequestRecipe(recipe json.RawMessage) error {
	var rc struct {
		Request struct {
			Method  string                 `json:"method"`
			URL     string                 `json:"url"`
			Headers map[string]interface{} `json:"headers"`
			Body    *string                `json:"body"` // 指针区分未提供与空串
		} `json:"request"`
	}
	if err := json.Unmarshal(recipe, &rc); err != nil {
		return fmt.Errorf("web 域 recipe 解析失败: %w", err)
	}
	req := rc.Request
	var missing []string
	if req.Method == "" {
		missing = append(missing, "method")
	}
	if req.URL == "" {
		missing = append(missing, "url")
	}
	if req.Headers == nil {
		missing = append(missing, "headers")
	}
	if req.Body == nil {
		missing = append(missing, "body")
	}
	if len(missing) > 0 {
		return fmt.Errorf("recipe.request 缺少必需字段: %v\n\n"+
			"request 必须四字段齐全（headers 可 {}、body 可 \"\"），url 须含 http:// 或 https://", missing)
	}
	if !strings.HasPrefix(req.URL, "http://") && !strings.HasPrefix(req.URL, "https://") {
		return fmt.Errorf("url 必须是完整 URL（包含 http:// 或 https://），当前 %q", req.URL)
	}
	return nil
}

// teachAssertDiscriminative 断言鉴别力的早期反馈，与复现通道的 assertDiscriminative
// 同一口径：只拒 body_contains 短子串（几乎必命中任意页面）。常规状态码等弱断言不在此
// 拦——鉴别力由 LLM 评估者结合 baseline 差分与完整响应终判；在此报错只会让收割侧保留
// 未矫正的原始配方，复现通道因形状错误直接判死，评估者反而看不到。
func teachAssertDiscriminative(assert json.RawMessage) error {
	var ac struct {
		BodyContains []string `json:"body_contains"`
	}
	if err := json.Unmarshal(assert, &ac); err != nil {
		return nil //nolint:nilerr // 结构留给复现通道判定，写入侧不做硬失败
	}
	for _, want := range ac.BodyContains {
		if len(strings.TrimSpace(want)) < MinBodyContainsLen {
			return fmt.Errorf("无鉴别力断言——body_contains 子串 %q 过短（几乎必命中任意页面）。\n\n"+
				"断言应捕捉利用响应独有特征：报错回显/泄露数据的完整子串（如 \"SQL syntax error\"、dump 出的字段值）", want)
		}
	}
	return nil
}
