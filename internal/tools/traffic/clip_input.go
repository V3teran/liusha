package traffic

import (
	"encoding/json"
	"net/url"
	"strings"

	"github.com/V3teran/liusha/internal/clipper"
	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/flow"
)

// fallback 截断阈值：caller 未注入 cfg.Classify 时使用，与 yaml 默认值
// (config.go applyClassifyDefaults) 同步——稳健激进方案。
const (
	fallbackMaxQueryValueLen      = 256
	fallbackMaxBodyStringValueLen = 256
	fallbackMaxRawBodyBytes       = 4096
	fallbackMaxResponseBodyBytes  = 8192
	fallbackResponseBodyTailBytes = 1024
)

// effectiveClassify 把 zero 字段 fallback 到对应常量。
// caller 通常用 cfg.Classify 经 ApplyDefaults 兜底后已无 zero 字段；保留兜底防止
// 测试构造空 ClassifyConfig 时出现 0 截断导致 LLM 输入完全空。
func effectiveClassify(c config.ClassifyConfig) config.ClassifyConfig {
	if c.MaxQueryValueLen <= 0 {
		c.MaxQueryValueLen = fallbackMaxQueryValueLen
	}
	if c.MaxBodyStringValueLen <= 0 {
		c.MaxBodyStringValueLen = fallbackMaxBodyStringValueLen
	}
	if c.MaxRawBodyBytes <= 0 {
		c.MaxRawBodyBytes = fallbackMaxRawBodyBytes
	}
	if c.MaxResponseBodyBytes <= 0 {
		c.MaxResponseBodyBytes = fallbackMaxResponseBodyBytes
	}
	if c.ResponseBodyTailBytes <= 0 {
		c.ResponseBodyTailBytes = fallbackResponseBodyTailBytes
	}
	return c
}

// sensitiveHeaderNames 是 value 会被 redact 的请求头集合（小写比较）。
// LLM 只需要看 key 识别 credential_locations，真凭证不该入 LLM context。
var sensitiveHeaderNames = map[string]struct{}{
	"cookie":          {},
	"authorization":   {},
	"x-token":         {},
	"x-auth-token":    {},
	"x-api-key":       {},
	"x-access-token":  {},
	"x-csrf-token":    {},
	"x-session":       {},
	"x-session-token": {},
}

// classifyInputData 是喂给 LLM 的截断后输入数据结构。
// 字段名与 SKILL.md 示例一致，便于 LLM 直接套例子。
type classifyInputData struct {
	Method          string            `json:"method"`
	URI             string            `json:"uri"`
	Status          int               `json:"status"`
	RequestHeaders  map[string]string `json:"request_headers"`
	QueryParams     map[string]string `json:"query_params,omitempty"`
	ResponseHeaders map[string]string `json:"response_headers"`
	ResponseBody    string            `json:"response_body"`
	RequestBody     json.RawMessage   `json:"request_body"`
}

// buildClassifyInput 把 flow 转成喂 LLM 的截断后结构。
// cfg 由 caller 从 cfg.Classify 注入（零值字段自动 fallback 到 v1 上线初始值）。
func buildClassifyInput(f flow.Flow, cfg config.ClassifyConfig) classifyInputData {
	c := effectiveClassify(cfg)
	uri, query := splitURIAndQuery(f.URL)
	return classifyInputData{
		Method:          f.Method,
		URI:             uri,
		Status:          f.StatusCode,
		RequestHeaders:  redactSensitiveHeaders(parseHeaders(f.RequestHeaders), c.MaxQueryValueLen),
		QueryParams:     clip.StringMap(query, c.MaxQueryValueLen),
		ResponseHeaders: pickContentType(parseHeaders(f.ResponseHeaders)),
		RequestBody:     clip.RequestBody(f.RequestBody, c.MaxBodyStringValueLen, c.MaxRawBodyBytes),
		ResponseBody:    clip.ResponseBody(f.ResponseBody, c.MaxResponseBodyBytes, c.ResponseBodyTailBytes),
	}
}

// splitURIAndQuery 把 full URL 拆成 (path[+'?'+raw_query], queryMap)。
// uri 保留 '?' 后内容（让 SKILL.md 的"URI 含 ?"判定逻辑成立），
// 同时 query map 单独提供给 LLM 看完整参数清单。
func splitURIAndQuery(rawURL string) (string, map[string]string) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL, nil
	}
	uri := u.Path
	if u.RawQuery != "" {
		uri += "?" + u.RawQuery
	}
	if len(u.Query()) == 0 {
		return uri, nil
	}
	out := make(map[string]string, len(u.Query()))
	for k, vs := range u.Query() {
		if len(vs) > 0 {
			out[k] = vs[0]
		}
	}
	return uri, out
}

// parseHeaders 把 jsonb headers 解析成 flat map[string]string。
// 兼容 flat 与 multi-value 两种格式（多值取首值）。
func parseHeaders(raw json.RawMessage) map[string]string {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err == nil {
		return m
	}
	var multi map[string][]string
	if err := json.Unmarshal(raw, &multi); err != nil {
		return nil
	}
	out := make(map[string]string, len(multi))
	for k, vs := range multi {
		if len(vs) > 0 {
			out[k] = vs[0]
		}
	}
	return out
}

// redactSensitiveHeaders 对敏感 header 把 value 替换为 "<redacted len=N>"；
// 其他 header value 截到 maxValueLen（防超长 cookie 撑爆 LLM context）。
func redactSensitiveHeaders(in map[string]string, maxValueLen int) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		if _, sensitive := sensitiveHeaderNames[strings.ToLower(k)]; sensitive {
			out[k] = clip.Redact(v)
			continue
		}
		out[k] = clip.String(v, maxValueLen)
	}
	return out
}

// pickContentType 只挑 Content-Type 头（其他 response header LLM 用不上，省 token）。
func pickContentType(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := map[string]string{}
	for k, v := range in {
		if strings.EqualFold(k, "Content-Type") {
			out["Content-Type"] = v
		}
	}
	return out
}

// stripJSONFence 剥离 LLM 常见的 markdown 代码块外壳（```json ... ``` 或 ``` ... ```）。
//
// 实测 deepseek/anthropic 在 prompt 要求"返回 JSON"时仍会习惯性加 fence；
// json.Unmarshal 看到反引号直接 fail。先 trim 空白，再剥前后 fence，再 trim 一次。
func stripJSONFence(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```json") {
		s = strings.TrimPrefix(s, "```json")
	} else if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```")
	}
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}
