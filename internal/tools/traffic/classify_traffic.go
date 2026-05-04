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

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/clip"
	"github.com/V3teran/liusha/internal/flow"
	"github.com/V3teran/liusha/internal/flowdecision"
	"github.com/V3teran/liusha/internal/llm"
	"github.com/V3teran/liusha/internal/logx"
	"github.com/V3teran/liusha/internal/skill"
	"github.com/V3teran/liusha/internal/toolfx"
)

// classifyTrafficLog 包级 logger（与 instrument.go 同样模式：避免每次 Execute 触发 logx.New）。
var classifyTrafficLog zerolog.Logger = logx.New("tools.classify_traffic")

// stripJSONFence 剥离 LLM 常见的 markdown 代码块外壳（```json ... ``` 或 ``` ... ```）。
//
// 实测 deepseek/anthropic 在 prompt 要求"返回 JSON"时仍会习惯性加 fence；
// json.Unmarshal 看到反引号直接 fail。先 trim 空白，再剥前后 fence，再 trim 一次。
// 没有 fence 时原样返回。
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

	// Decisions 可空：装配时注入则把 LLM 输出 best-effort 落 flow_decision；
	// nil 退化为旧行为（仅透传 Output 给主 ReAct，不持久化）。
	// EngagementID 同时为空时也跳过落库（无外键归属）。
	Decisions    *flowdecision.Store
	EngagementID string
}

// classifyOutput 与 skills/classify-traffic/SKILL.md 约定的 LLM 输出 JSON 一一对应。
// jsonb 字段以 RawMessage 透传，避免 LLM 出意外结构时落库失败（容错优先）。
type classifyOutput struct {
	Operation           string          `json:"operation"`
	ResourceScope       string          `json:"resource_scope"`
	AttackSurfaces      json.RawMessage `json:"attack_surfaces"`
	CarriesAuth         bool            `json:"carries_auth"`
	CredentialLocations json.RawMessage `json:"credential_locations"`
	RequiredSkills      json.RawMessage `json:"required_skills"`
	Reasoning           string          `json:"reasoning"`
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
func (a *ClassifyTraffic) Execute(ctx context.Context, args json.RawMessage) (toolfx.Result, error) {
	var in struct {
		FlowID int64 `json:"flow_id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return toolfx.Result{}, fmt.Errorf("decode args: %w", err)
	}
	if in.FlowID <= 0 {
		return toolfx.Result{}, errors.New("flow_id 必填且 > 0")
	}
	if a.LLM == nil || a.Flows == nil || a.Loader == nil {
		return toolfx.Result{}, errors.New("ClassifyTraffic: LLM/Flows/Loader 都必须装配")
	}

	f, err := a.Flows.GetByID(ctx, in.FlowID)
	if err != nil {
		return toolfx.Result{}, fmt.Errorf("拉取 flow %d: %w", in.FlowID, err)
	}

	card, err := a.Loader.Load(classifyTrafficSkillName)
	if err != nil {
		return toolfx.Result{}, fmt.Errorf("加载 classify-traffic skill: %w", err)
	}

	input := buildClassifyInput(f)
	inputJSON, err := json.MarshalIndent(input, "", "  ")
	if err != nil {
		return toolfx.Result{}, fmt.Errorf("序列化截断后流量: %w", err)
	}

	prompt := strings.Replace(card.Body, "$INPUT_DATA$", string(inputJSON), 1)

	res, err := a.LLM.Generate(ctx, []llm.Message{{Role: llm.RoleUser, Content: prompt}}, nil)
	if err != nil {
		return toolfx.Result{}, fmt.Errorf("classify_traffic LLM: %w", err)
	}

	// best-effort 把 LLM 输出落 flow_decision（非阻塞 Execute 返回）；
	// 失败仅 warn，行为对外不变。
	a.appendDecision(in.FlowID, res.Content)

	// 透传 LLM 原始 content 给主 ReAct LLM；主 LLM 解析里面的 required_skills /
	// credential_locations 后做下一步决策（短路 done 或 delegate）。
	return toolfx.Result{
		Output:  json.RawMessage(res.Content),
		Summary: fmt.Sprintf("classify_traffic flow=%d %s %s", f.ID, f.Method, f.URL),
	}, nil
}

// appendDecision 解析 LLM JSON 输出并 best-effort 落 flow_decision。
//
// 任何失败（store 未注入 / EngagementID 为空 / JSON 解析失败 / DB 写入失败）
// 都仅打 warn 日志，不返回错误。落库用 context.Background() 与业务 ctx 解耦，
// 避免业务 ctx 取消时埋点丢失（与 internal/llm/instrument.go 同样策略）。
//
// resource_scope 不属于 enum 集合时强制回退为 "unknown"（CHECK 约束兜底）。
func (a *ClassifyTraffic) appendDecision(flowID int64, content string) {
	if a.Decisions == nil || a.EngagementID == "" {
		return
	}
	cleaned := stripJSONFence(content)
	var out classifyOutput
	if err := json.Unmarshal([]byte(cleaned), &out); err != nil {
		classifyTrafficLog.Warn().Err(err).Int64("flow_id", flowID).
			Msg("flow_decision: 解析 LLM JSON 失败，跳过落库")
		return
	}
	scope := flowdecision.ResourceScope(out.ResourceScope)
	switch scope {
	case flowdecision.ResourceScopePrivate,
		flowdecision.ResourceScopePublic,
		flowdecision.ResourceScopeUnknown:
	default:
		scope = flowdecision.ResourceScopeUnknown
	}
	if _, err := a.Decisions.Append(context.Background(), flowdecision.Decision{
		EngagementID:        a.EngagementID,
		FlowID:              flowID,
		Operation:           out.Operation,
		ResourceScope:       scope,
		AttackSurfaces:      out.AttackSurfaces,
		CarriesAuth:         out.CarriesAuth,
		CredentialLocations: out.CredentialLocations,
		RequiredSkills:      out.RequiredSkills,
		Reasoning:           out.Reasoning,
	}); err != nil {
		classifyTrafficLog.Warn().Err(err).Int64("flow_id", flowID).
			Msg("flow_decision: 写库失败（不阻塞 Execute）")
	}
}

// buildClassifyInput 把 flow 转成喂 LLM 的截断后结构。
//
// 截断策略详见 internal/clip 包；保守阈值（query/body string ≤100 字符，response body 4KB）。
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
			out[k] = clip.Redact(v)
			continue
		}
		out[k] = clip.String(v, maxValueLen)
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

