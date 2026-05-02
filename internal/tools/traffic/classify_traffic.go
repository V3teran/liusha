// Package traffic 提供主 ReAct 的流量业务工具：classify_traffic / get_findings。
//
// 与 tools/spawn（派发器）拆分：traffic 包内是分析/查询流量的具体业务工具，
// 不持有 skill builder 类型（已迁到 internal/skill 包）。
package traffic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/V3teran/liusha/internal/flow"
	"github.com/V3teran/liusha/internal/llm"
	"github.com/V3teran/liusha/internal/skill"
	"github.com/V3teran/liusha/internal/tool"
)

// FlowReader 是 classify_traffic 依赖的最小 flow 读接口，由 *flow.Store 自动满足。
// 局部定义在 consumer 侧（Go idiom: accept interfaces, return structs）。
type FlowReader interface {
	GetByID(ctx context.Context, id int64) (flow.Flow, error)
}

// classifyTrafficSkillName 是本工具加载 prompt body 的 skill name。
//
// 该 skill 是"内部 prompt"——loader 自动 Index 它，但 main.go 装配 delegate.Catalog 时
// 必须过滤掉它，避免主 LLM 误以为它是可 delegate 的子 skill。
const classifyTrafficSkillName = "classify-traffic"

// 截断阈值（保守版）：在节省 token 与保留 LLM 判断信号之间偏后者。
//
// 关键判断点对截断敏感度：
//   - credential_locations（认证字段名）—— 只看 key，截断不影响
//   - attack_surfaces（query/body 结构 + Content-Type）—— 看 keys + Content-Type，截断不影响
//   - resource_scope（私有 vs 公开）—— **依赖 response_body 业务数据语义**，截短会损失判断质量
//   - operation（业务语义）—— 依赖 url + body 语义
//
// 策略：keys 全保留；string value 给足空间（>= 100 字符能完整体现业务字段语义）；
//      response_body 4KB（前 3.5KB + 尾 0.5KB），保留分页 / 总数 / 用户列表全貌；
//      敏感 header value 仍直接 redact（安全要求，不是性能优化）。
//
// 预估单次 LLM input：~1500-3000 token（含 SKILL.md prompt ~3000 token + 流量数据 ~1500 token）。
const (
	// maxQueryValueLen 单个 query value 截断长度（保留完整 token 结构便于 LLM 判断 attack_surfaces）
	maxQueryValueLen = 100
	// maxBodyStringValueLen JSON body 中 string value 截断长度（保留业务字段语义）
	maxBodyStringValueLen = 100
	// maxRawBodyBytes 非 JSON request body 的截断字节数
	maxRawBodyBytes = 2048
	// maxResponseBodyBytes 响应 body 的截断字节数（前缀 + 尾部一起算）
	maxResponseBodyBytes = 4096
	// responseBodyTailBytes 截断后保留的尾部字节数（捕获分页 / 总数 / 用户列表尾等关键信号）
	responseBodyTailBytes = 512
)

// sensitiveHeaderNames 是会被 redact value 的请求头集合（小写比较）。
// LLM 只需要看到这些 header 的 key（用于识别 credential_locations），
// value 是真凭证不该入 LLM context。
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

// ClassifyTraffic 让 LLM 一次性判定流量类型 + 输出可能漏洞清单 + 凭证位置。
//
// 业务背景：主 ReAct 第 1 步先调它，工具内部按 flow_id 拉完整 flow（请求/响应都拿全），
// 智能截断后拼 prompt（来自 skills/classify-traffic/SKILL.md）发给 LLM，
// 解析返回 JSON：{operation, resource_scope, attack_surfaces, carries_auth,
// credential_locations, required_skills, reasoning}。
//
// 输出会被主 ReAct 用来：
//   - required_skills 为空 → done({"reason":"no_required_skills"}) 短路
//   - 否则按 required_skills 调 delegate(skill_name, flow_id, credential_locations)
type ClassifyTraffic struct {
	LLM    llm.Generator
	Flows  FlowReader
	Loader *skill.Loader
}

// Name 返回工具名 "classify_traffic"。
func (a *ClassifyTraffic) Name() string { return "classify_traffic" }

// Description 给 LLM 的工具描述。
func (a *ClassifyTraffic) Description() string {
	return "分析单条 HTTP 流量，输出 JSON: {operation, resource_scope, attack_surfaces, " +
		"carries_auth, credential_locations, required_skills, reasoning}。" +
		"工具内部按 flow_id 拉完整流量并智能截断后调 LLM；调用方只需传 flow_id。"
}

// ParametersJSON 工具入参 JSON Schema：极简化，只需 flow_id。
//
// 完整流量数据由工具内部从 flow store 拉取，避免主 LLM 在不知道 headers/body
// 内容的情况下被迫"瞎传字段"。
func (a *ClassifyTraffic) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{
        "type":"object",
        "properties":{
            "flow_id":{"type":"integer","description":"http_flow.id（int64）"}
        },
        "required":["flow_id"]
    }`)
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

// Execute 解析 args（仅 flow_id）→ 拉 flow → 智能截断 → 拼 prompt → 调 LLM → 返回原始 JSON content。
func (a *ClassifyTraffic) Execute(ctx context.Context, args json.RawMessage) (tool.Result, error) {
	var in struct {
		FlowID int64 `json:"flow_id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return tool.Result{}, fmt.Errorf("decode args: %w", err)
	}
	if in.FlowID <= 0 {
		return tool.Result{}, errors.New("flow_id 必填且 > 0")
	}
	if a.LLM == nil || a.Flows == nil || a.Loader == nil {
		return tool.Result{}, errors.New("ClassifyTraffic: LLM/Flows/Loader 都必须装配")
	}

	f, err := a.Flows.GetByID(ctx, in.FlowID)
	if err != nil {
		return tool.Result{}, fmt.Errorf("拉取 flow %d: %w", in.FlowID, err)
	}

	card, err := a.Loader.Load(classifyTrafficSkillName)
	if err != nil {
		return tool.Result{}, fmt.Errorf("加载 classify-traffic skill: %w", err)
	}

	input := buildClassifyInput(f)
	inputJSON, err := json.MarshalIndent(input, "", "  ")
	if err != nil {
		return tool.Result{}, fmt.Errorf("序列化截断后流量: %w", err)
	}

	prompt := strings.Replace(card.Body, "$INPUT_DATA$", string(inputJSON), 1)

	res, err := a.LLM.Generate(ctx, []llm.Message{{Role: llm.RoleUser, Content: prompt}}, nil)
	if err != nil {
		return tool.Result{}, fmt.Errorf("classify_traffic LLM: %w", err)
	}

	// 透传 LLM 原始 content 给主 ReAct LLM；主 LLM 解析里面的 required_skills /
	// credential_locations 后做下一步决策（短路 done 或 delegate）。
	return tool.Result{
		Output:  json.RawMessage(res.Content),
		Summary: fmt.Sprintf("classify_traffic flow=%d %s %s", f.ID, f.Method, f.URL),
	}, nil
}

// buildClassifyInput 把 flow 转成喂 LLM 的截断后结构。
//
// 截断策略（激进）：
//   - request_headers：完整 keys；敏感 header（Cookie/Authorization 等）value → "<redacted len=N>"；其他 value 截 30 字符
//   - query_params：完整 keys；value 截 30 字符
//   - request_body：JSON 解析成功 → keys 全保留，string value 截 20 字符；非 JSON → 截 256 字节
//   - response_headers：只保留 Content-Type
//   - response_body：截 512 字节（前 400 + 尾部 112，捕获分页 / 总数信号）
func buildClassifyInput(f flow.Flow) classifyInputData {
	uri, query := splitURIAndQuery(f.URL)
	return classifyInputData{
		Method:          f.Method,
		URI:             uri,
		Status:          f.StatusCode,
		RequestHeaders:  redactSensitiveHeaders(parseHeaders(f.RequestHeaders), maxQueryValueLen),
		QueryParams:     truncateStringMap(query, maxQueryValueLen),
		ResponseHeaders: pickContentType(parseHeaders(f.ResponseHeaders)),
		RequestBody:     summarizeRequestBody(f.RequestBody),
		ResponseBody:    summarizeResponseBody(f.ResponseBody),
	}
}

// splitURIAndQuery 把 full URL 拆成 (path[+'?'+raw_query], queryMap)。
//
// 注意：返回的 uri 保留 '?' 后内容（让 SKILL.md 的"URI 含 ?"判定逻辑成立），
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
// 兼容 v1 proxy 当前的 flat 格式；多值 header 取首值（classify 只关心 key 是否存在）。
func parseHeaders(raw json.RawMessage) map[string]string {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err == nil {
		return m
	}
	// 多值格式 fallback
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

// redactSensitiveHeaders 对敏感 header 把 value 替换为 "<redacted len=N>"，
// 其他 header value 截到 maxValueLen。
func redactSensitiveHeaders(in map[string]string, maxValueLen int) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		if _, sensitive := sensitiveHeaderNames[strings.ToLower(k)]; sensitive {
			out[k] = fmt.Sprintf("<redacted len=%d>", len(v))
			continue
		}
		out[k] = truncateString(v, maxValueLen)
	}
	return out
}

// pickContentType 从全部 response headers 中只挑 Content-Type 出来（其他 LLM 用不上，省 token）。
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

// truncateStringMap 对 map 中所有 value 截到 max 字符。
func truncateStringMap(in map[string]string, max int) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = truncateString(v, max)
	}
	return out
}

// summarizeRequestBody 处理请求 body：
//   - 空 → 返回 `{}`
//   - JSON → 解析后保留所有 key，string value 截 20 字符；其他类型保留
//   - 非 JSON → 截 256 字节，包成 {"_raw":"..."} 给 LLM
func summarizeRequestBody(body []byte) json.RawMessage {
	if len(body) == 0 {
		return json.RawMessage(`{}`)
	}
	var parsed any
	if err := json.Unmarshal(body, &parsed); err == nil {
		shrunk := shrinkJSONValue(parsed, maxBodyStringValueLen)
		out, err := json.Marshal(shrunk)
		if err == nil {
			return out
		}
	}
	raw := truncateString(string(body), maxRawBodyBytes)
	out, _ := json.Marshal(map[string]string{"_raw": raw})
	return out
}

// shrinkJSONValue 递归遍历 JSON 值，把所有 string 截到 max 字符；其他类型不动。
func shrinkJSONValue(v any, max int) any {
	switch t := v.(type) {
	case string:
		return truncateString(t, max)
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = shrinkJSONValue(val, max)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = shrinkJSONValue(val, max)
		}
		return out
	default:
		return v
	}
}

// summarizeResponseBody 截响应 body 到 maxResponseBodyBytes：
//   - len <= max → 原样返回
//   - 否则前 (max - tail) 字节 + "...<truncated>..." + 尾部 tail 字节
func summarizeResponseBody(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	if len(body) <= maxResponseBodyBytes {
		return string(body)
	}
	headLen := maxResponseBodyBytes - responseBodyTailBytes
	if headLen < 0 {
		headLen = 0
	}
	return string(body[:headLen]) + "...<truncated>..." + string(body[len(body)-responseBodyTailBytes:])
}

// truncateString 把字符串截到 n 个字符（粗截断；按 byte 计数）。
func truncateString(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
