package traffic

import (
	"encoding/json"
	"net/url"
	"strings"

	"github.com/V3teran/liusha/internal/clip"
	"github.com/V3teran/liusha/internal/flow"
)

// 截断阈值（保守版）：在节省 token 与保留 LLM 判断信号之间偏后者。
//
// 关键判断点对截断敏感度：
//   - credential_locations（认证字段名）：只看 key，截断不影响
//   - attack_surfaces（query/body 结构 + Content-Type）：看 keys + Content-Type，截断不影响
//   - resource_scope（私有 vs 公开）：依赖 response_body 业务数据语义，截短会损失判断质量
//   - operation（业务语义）：依赖 url + body 语义
//
// 策略：keys 全保留；string value 给足空间（≥100 字符体现业务字段语义）；
// response_body 4KB（前 3.5KB + 尾 0.5KB）保留分页/总数/列表全貌；
// 敏感 header value 直接 redact（安全要求，非性能优化）。
const (
	maxQueryValueLen      = 100  // 单个 query value 截断长度
	maxBodyStringValueLen = 100  // JSON body 中 string value 截断长度
	maxRawBodyBytes       = 2048 // 非 JSON request body 截断字节数
	maxResponseBodyBytes  = 4096 // 响应 body 截断字节数（前缀 + 尾部）
	responseBodyTailBytes = 512  // 截断后保留的尾部字节数
)

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
func buildClassifyInput(f flow.Flow) classifyInputData {
	uri, query := splitURIAndQuery(f.URL)
	return classifyInputData{
		Method:          f.Method,
		URI:             uri,
		Status:          f.StatusCode,
		RequestHeaders:  redactSensitiveHeaders(parseHeaders(f.RequestHeaders), maxQueryValueLen),
		QueryParams:     clip.StringMap(query, maxQueryValueLen),
		ResponseHeaders: pickContentType(parseHeaders(f.ResponseHeaders)),
		RequestBody:     clip.RequestBody(f.RequestBody, maxBodyStringValueLen, maxRawBodyBytes),
		ResponseBody:    clip.ResponseBody(f.ResponseBody, maxResponseBodyBytes, responseBodyTailBytes),
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
