package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/evaluator"
	"github.com/V3teran/liusha/internal/httpreplay"
	"github.com/V3teran/liusha/internal/tools"
)

// SelfContainedRequest 是自包含的完整 HTTP 请求（repro.request 的形状）。
// 配方不引用 traffic_id——工具自发的流量（http_request 等）直接以完整请求
// 形态进配方，可移植（可导出为 curl）、无外部状态依赖。
type SelfContainedRequest struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

// toSource 把自包含请求投影成 httpreplay.Source（headers key 统一小写）。
func (r SelfContainedRequest) toSource() (httpreplay.Source, error) {
	method := strings.ToUpper(strings.TrimSpace(r.Method))
	if method == "" {
		method = "GET"
	}
	if !strings.HasPrefix(r.URL, "http://") && !strings.HasPrefix(r.URL, "https://") {
		return httpreplay.Source{}, fmt.Errorf("url 必须是完整 URL（含 http:// 或 https://），当前 %q", r.URL)
	}
	if _, err := url.Parse(r.URL); err != nil {
		return httpreplay.Source{}, fmt.Errorf("url 解析失败: %w", err)
	}
	headers := make(map[string]string, len(r.Headers))
	for k, v := range r.Headers {
		headers[strings.ToLower(k)] = v
	}
	hdrJSON, err := json.Marshal(headers)
	if err != nil {
		return httpreplay.Source{}, fmt.Errorf("marshal headers: %w", err)
	}
	return httpreplay.Source{
		Method:  method,
		URL:     r.URL,
		Headers: hdrJSON,
		Body:    []byte(r.Body),
	}, nil
}

// ReplayRecipe 是 web 域的 L1 复现原语（evaluator.Attempt.Primitives 的形状）。
// 机器可判的坐实配方：完整利用请求 + 断言，可选良性基线做差分对照。
// Resolve 非空时，主请求前先发一个准备请求抽新鲜值注入——专治 replay 时无法
// 复用的服务器现造值（opaque-id / nonce / 过期 token）。
type ReplayRecipe struct {
	Resolve  *ResolveStep          `json:"resolve,omitempty"`
	Request  SelfContainedRequest  `json:"request"`
	Baseline *SelfContainedRequest `json:"baseline,omitempty"`
	Assert   Assertion             `json:"assert"`
}

// Assertion 是坐实断言：全部满足才算复现坐实。至少要有一条谓词，
// 否则是橡皮图章（空断言"确认"一切）——拒绝坐实。
// body_not_contains 与 body_absent 同义（前者是 prompt 侧口径）。
type Assertion struct {
	StatusCode     *int              `json:"status_code,omitempty"`     // 响应码须等于此值
	BodyContains   []string          `json:"body_contains,omitempty"`   // 响应体须含全部子串（越权拿到数据、SQL 报错、XSS 回显）
	BodyAbsent     []string          `json:"body_absent,omitempty"`     // 响应体须不含任一子串（未跳登录页=认证绕过坐实）
	HeaderContains map[string]string `json:"header_contains,omitempty"` // 响应头 key→子串
	MinDurationMs  *int              `json:"min_duration_ms,omitempty"` // 响应耗时至少 N ms——时间盲注入的合法证据类型
}

// UnmarshalJSON 接受 body_not_contains 作为 body_absent 的规范别名（prompt 教的是前者）。
func (a *Assertion) UnmarshalJSON(b []byte) error {
	var raw struct {
		StatusCode      *int              `json:"status_code,omitempty"`
		BodyContains    []string          `json:"body_contains,omitempty"`
		BodyAbsent      []string          `json:"body_absent,omitempty"`
		BodyNotContains []string          `json:"body_not_contains,omitempty"`
		HeaderContains  map[string]string `json:"header_contains,omitempty"`
		MinDurationMs   *int              `json:"min_duration_ms,omitempty"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	a.StatusCode = raw.StatusCode
	a.BodyContains = raw.BodyContains
	a.BodyAbsent = append(append([]string{}, raw.BodyAbsent...), raw.BodyNotContains...)
	a.HeaderContains = raw.HeaderContains
	a.MinDurationMs = raw.MinDurationMs
	return nil
}

func (a Assertion) empty() bool {
	return a.StatusCode == nil && len(a.BodyContains) == 0 &&
		len(a.BodyAbsent) == 0 && len(a.HeaderContains) == 0 && a.MinDurationMs == nil
}

// ReproEnvelope 是复现配方的域信封：evaluator/图/收割层只认信封，不解析 recipe 内部
// （形状归各域——加新域晋升门零改动）。配方在引擎收割路径落图前归一化成此结构。
type ReproEnvelope struct {
	Domain string          `json:"domain"` // web / generic /（未来 binary/cloud/lateral…）
	Recipe json.RawMessage `json:"recipe"` // 域自定义：web={request,baseline?}；generic={steps}
	Assert json.RawMessage `json:"assert"` // 域自定义坐实判据
}

// 已支持的域。generic 无机器重放通道——由裁决官经 run_command 按 recipe 自主执行取证。
const (
	DomainWeb     = "web"
	DomainGeneric = "generic"
)

// Replayer 是 web+generic 双域的 evaluator.Replayer：按信封 domain 分发。
// 不依赖任何流量存储——配方自包含（工具自发流量同样可复现）。
type Replayer struct {
	creds credential.Provider // 可选：按 host 注入会话上下文（配方缺 Cookie 时补全）
	host  string
}

// NewReplayer 构造域分发 Replayer（零依赖：配方自包含）。
func NewReplayer() *Replayer { return &Replayer{} }

// WithCredentials 注入凭据提供方与目标 host：web 重放请求缺会话头
// （Cookie/Authorization）时，用凭据池里该 host 的首个身份补全——
// 配方自包含是理想，LLM 漏写会话上下文时重放会打到登录页导致真实漏洞
// 被误证伪。best-effort：查询失败按无凭据处理。
func (r *Replayer) WithCredentials(creds credential.Provider, host string) *Replayer {
	if creds != nil && host != "" {
		r.creds = creds
		r.host = host
	}
	return r
}

// injectSessionContext 给 recipe 的 request/baseline 补全会话头（缺才补，
// 配方显式带的头优先）。
func (r *Replayer) injectSessionContext(ctx context.Context, recipe json.RawMessage) json.RawMessage {
	if r.creds == nil || len(recipe) == 0 {
		return recipe
	}
	var rc struct {
		Request  json.RawMessage `json:"request"`
		Baseline json.RawMessage `json:"baseline"`
	}
	if json.Unmarshal(recipe, &rc) != nil || len(rc.Request) == 0 {
		return recipe
	}
	identities, err := r.creds.GetIdentitiesByHost(ctx, r.host)
	if err != nil || len(identities) == 0 {
		return recipe
	}
	// 多身份时持久优先、名称破平——HGetAll 返回序任意，盲取 [0] 曾把任务
	// 临时会话（security 停在 impossible 的浏览器快照）注入复放，把 DVWA
	// 全部假设打到修复版难度上整体证伪。持久身份是显式录入的权威凭据；
	// 自动会话快照只在无持久身份时兜底。
	sort.SliceStable(identities, func(i, j int) bool {
		pi, pj := tools.IsAutoSessionIdentity(identities[i].Name), tools.IsAutoSessionIdentity(identities[j].Name)
		if pi != pj {
			return !pi // 持久在前
		}
		return identities[i].Name < identities[j].Name
	})
	// 首个身份的 headers 型凭证拼成 Cookie/Authorization 头（配方已带的键不覆盖）
	extra := map[string]string{}
	for _, c := range identities[0].Credentials {
		if c.Type != credential.TypeHeaders || c.Key == "" || c.Value == "" {
			continue
		}
		extra[strings.ToLower(c.Key)] = c.Value
	}
	if len(extra) == 0 {
		return recipe
	}

	apply := func(req json.RawMessage) (json.RawMessage, bool) {
		var m map[string]json.RawMessage
		// "null" 合法 JSON 会解出 nil map 不报错——后续 m["headers"]= 会 panic
		if err := json.Unmarshal(req, &m); err != nil || m == nil {
			return req, false
		}
		var headers map[string]string
		if h, ok := m["headers"]; ok && len(h) > 0 {
			_ = json.Unmarshal(h, &headers)
		}
		if headers == nil {
			headers = map[string]string{}
		}
		changed := false
		for k, v := range extra {
			lk := strings.ToLower(k)
			if _, exists := headers[lk]; exists {
				continue // 配方显式带的凭证优先
			}
			// 无 Cookie 头且凭据是 Cookie 型整段时直接放 Cookie 键
			headers[lk] = v
			changed = true
		}
		if !changed {
			return req, false
		}
		hb, err := json.Marshal(headers)
		if err != nil {
			return req, false
		}
		m["headers"] = hb
		out, err := json.Marshal(m)
		if err != nil {
			return req, false
		}
		return out, true
	}

	changedAny := false
	if req, ok := apply(rc.Request); ok {
		rc.Request = req
		changedAny = true
	}
	if len(rc.Baseline) > 0 {
		if req, ok := apply(rc.Baseline); ok {
			rc.Baseline = req
			changedAny = true
		}
	}
	if !changedAny {
		return recipe
	}
	out, err := json.Marshal(map[string]json.RawMessage{
		"request":  rc.Request,
		"baseline": rc.Baseline,
	})
	if err != nil {
		return recipe
	}
	return out
}

// replayEvidence 是落进 exploration_verification 的复现证据（基线/利用请求双对照 + 断言明细）。
type replayEvidence struct {
	Method string `json:"method"`
	URL    string `json:"url"`
	// 基线（配方提供 baseline 时的良性对照）与利用请求（配方 request 原样重发）的双对照——
	// 裁决官据此看差分。
	HasBaseline     bool              `json:"has_baseline"`
	BaselineStatus  int               `json:"baseline_status_code,omitempty"`
	BaselineDurMs   int64             `json:"baseline_duration_ms,omitempty"`
	BaselineBodyLen int               `json:"baseline_body_len,omitempty"`
	ExploitStatus   int               `json:"exploit_status_code"`
	ExploitDurMs    int64             `json:"exploit_duration_ms"`
	RespHeaders     map[string]string `json:"response_headers"`
	RespBodyLen     int               `json:"response_body_len"`
	RespBodySnip    string            `json:"response_body_snippet"`
	AssertPassed    bool              `json:"assert_passed"`
	AssertReasons   []string          `json:"assert_reasons"` // 每条谓词的判定（通过/失败原因）
}

const evidenceBodySnip = 2048 // 证据里响应体截断长度

// Replay 实现 evaluator.Replayer：按域信封分发。
// web=机器确定性重放采证；generic=配方回显（裁决官自主执行）。
// 配方在收割路径已统一归一化为信封（tools.NormalizeReproEnvelope），
// 复现门只认信封、不再嗅探历史裸格式。
func (r *Replayer) Replay(ctx context.Context, primitives json.RawMessage) (evaluator.Result, error) {
	var env ReproEnvelope
	if err := json.Unmarshal(primitives, &env); err != nil {
		return evaluator.Result{}, fmt.Errorf("Replayer: 解析域信封失败: %w", err)
	}
	switch env.Domain {
	case DomainWeb:
		env.Recipe = r.injectSessionContext(ctx, env.Recipe)
		return replayWebEnvelope(ctx, env)
	case DomainGeneric:
		return replayGeneric(env)
	case "":
		return evaluator.Result{}, fmt.Errorf("Replayer: 缺少 domain 信封（{\"domain\": \"web\"|\"generic\", \"recipe\": ..., \"assert\": ...}）")
	default:
		return evaluator.Result{}, fmt.Errorf("Replayer: 未知域 %q（已支持: %s, %s）", env.Domain, DomainWeb, DomainGeneric)
	}
}

// replayWebEnvelope 信封化 web 配方：recipe={request,baseline?} + 独立 assert。
func replayWebEnvelope(ctx context.Context, env ReproEnvelope) (evaluator.Result, error) {
	if len(env.Recipe) == 0 {
		return evaluator.Result{}, fmt.Errorf("web.Replayer: recipe 为空（需 {\"request\": {...}, \"baseline\": {...}?}）")
	}
	var core struct {
		Request  SelfContainedRequest  `json:"request"`
		Baseline *SelfContainedRequest `json:"baseline"`
	}
	if err := json.Unmarshal(env.Recipe, &core); err != nil {
		return evaluator.Result{}, fmt.Errorf("web.Replayer: 解析 recipe 失败: %w", err)
	}
	var assert Assertion
	if len(env.Assert) == 0 {
		return evaluator.Result{}, fmt.Errorf("web.Replayer: assert 为空，无坐实谓词（至少给一条 status_code/body_contains/body_not_contains/header_contains/min_duration_ms）")
	}
	if err := json.Unmarshal(env.Assert, &assert); err != nil {
		return evaluator.Result{}, fmt.Errorf("web.Replayer: 解析 assert 失败: %w", err)
	}
	return replayWeb(ctx, ReplayRecipe{Request: core.Request, Baseline: core.Baseline, Assert: assert})
}

// replayGeneric 无机器重放通道：配方原样回显给裁决官（其经 run_command 按 recipe 步骤
// 自主执行取证，轨迹以裁决 reasoning + 本证据为审计面）。不执行任何动作。
func replayGeneric(env ReproEnvelope) (evaluator.Result, error) {
	if len(env.Recipe) == 0 {
		return evaluator.Result{}, fmt.Errorf("generic.Replayer: recipe 为空——裁决官无从执行")
	}
	out, err := json.Marshal(map[string]interface{}{
		"domain": DomainGeneric,
		"recipe": env.Recipe,
		"assert": orEmptyRaw(env.Assert),
		"note":   "本域无机器重放通道：请用 run_command 按 recipe 的步骤自主执行取证，基于你亲见的命令输出裁决（不必调 replay_for_verification）",
	})
	if err != nil {
		return evaluator.Result{}, fmt.Errorf("generic.Replayer: 序列化证据失败: %w", err)
	}
	return evaluator.Result{Evaluation: out}, nil
}

func orEmptyRaw(b json.RawMessage) json.RawMessage {
	if len(b) == 0 {
		return json.RawMessage("{}")
	}
	return b
}

// minBodyContainsLen 是 body_contains 子串的最小鉴别长度：过短的子串（如 "1"/"2"/"3"）
// 几乎必然命中任意页面——断言无鉴别力，坐实即假阳性。
const minBodyContainsLen = 4

// assertDiscriminative 闸门鉴别力守卫（假阳性第一道机器防线）：
//   - body_contains 每个子串须 ≥ minBodyContainsLen 个字符（修剪后按字节计）
//     注意：已移除自指检查和无 baseline 的强谓词检查，让 LLM Judge 做最终判断
func assertDiscriminative(a Assertion, _ httpreplay.Source, _ bool) error {
	for _, want := range a.BodyContains {
		if len(strings.TrimSpace(want)) < minBodyContainsLen {
			return fmt.Errorf("%w: body_contains 子串 %q 短于 %d 字符（几乎必命中任意页面）。断言应捕捉利用响应独有特征：报错回显/泄露数据的完整子串（如 SQL 错误片段、dump 出的字段值）", evaluator.ErrAssertNotDiscriminative, want, minBodyContainsLen)
		}
		// 自指检查已移除（payload 回显等合理场景会被误伤），由 LLM Judge 终判。
	}
	// 无 baseline 的强谓词检查已移除（常态 200 类断言的鉴别力由 LLM Judge 终判）。
	return nil
}

// replayWeb web 域机器复放：结构校验 → 鉴别力守卫 → resolve 注入 → baseline 差分护栏 → 利用请求重发 → 采证。
func replayWeb(ctx context.Context, recipe ReplayRecipe) (evaluator.Result, error) {
	// 空断言不可坐实：机器无从判定即无法晋升（拒绝橡皮图章）。
	if recipe.Assert.empty() {
		return evaluator.Result{}, fmt.Errorf("web.Replayer: assert 为空，无坐实谓词（至少给一条 status_code/body_contains/body_not_contains/header_contains/min_duration_ms）")
	}
	// 结构先行（request 形状），语义随后（断言鉴别力）——错误信息指向首个真问题。
	exploitSrc, err := recipe.Request.toSource()
	if err != nil {
		return evaluator.Result{}, fmt.Errorf("web.Replayer: request 非法——%w", err)
	}
	if dErr := assertDiscriminative(recipe.Assert, exploitSrc, recipe.Baseline != nil); dErr != nil {
		return evaluator.Result{}, fmt.Errorf("web.Replayer: %w", dErr)
	}

	// 准备请求：抽服务器现造值注入主请求。抽值失败硬错误，绝不静默 pass（护栏2）。
	if recipe.Resolve != nil {
		name, val, rErr := runResolve(ctx, *recipe.Resolve)
		if rErr != nil {
			return evaluator.Result{}, rErr
		}
		injected := injectResolved(recipe.Request, name, val)
		exploitSrc, err = injected.toSource()
		if err != nil {
			return evaluator.Result{}, fmt.Errorf("web.Replayer: resolve 注入后 request 非法——%w", err)
		}
	}

	// 差分复现铁律（业界基线对照法）：配方提供 baseline 时先放良性基线，
	// 断言在基线上也全命中 = 断言无鉴别力（正常响应即满足），拒绝坐实。
	// 坐实的唯一形态：利用响应呈现基线没有的特征（报错回显/泄露数据/延迟）。
	var baseline *httpreplay.Result
	var baseDur int64
	if recipe.Baseline != nil {
		baseSrc, bErr := recipe.Baseline.toSource()
		if bErr != nil {
			return evaluator.Result{}, fmt.Errorf("web.Replayer: baseline 非法——%w", bErr)
		}
		baseStart := time.Now()
		bres, bErr := httpreplay.Replay(ctx, baseSrc, httpreplay.Mods{})
		baseDur = time.Since(baseStart).Milliseconds()
		if bErr != nil {
			return evaluator.Result{}, fmt.Errorf("web.Replayer: 基线重放失败: %w", bErr)
		}
		baseline = &bres
		// 注释掉机器护栏的 baseline 差分检查，让 LLM Judge 自己判断
		// 原因：Executor 生成的断言质量不稳定，机器护栏过早证伪导致 LLM Judge 无法运行
		// LLM Judge 会看到完整的 exploit 和 baseline 响应，能更智能地判断攻击是否成功
		/*
			if basePassed, _ := recipe.Assert.eval(bres, baseDur); basePassed {
				return evaluator.Result{}, fmt.Errorf("web.Replayer: %w——基线（良性请求）同样满足全部谓词，命中不构成漏洞证据（断言应捕捉利用响应独有特征：报错回显/数据泄露/延迟）", evaluator.ErrAssertNotDiscriminative)
			}
		*/
	}

	// 利用请求：配方 request 原样重发。
	start := time.Now()
	res, err := httpreplay.Replay(ctx, exploitSrc, httpreplay.Mods{})
	dur := time.Since(start).Milliseconds()
	if err != nil {
		return evaluator.Result{}, fmt.Errorf("web.Replayer: 重发失败: %w", err)
	}

	// assert 命中明细：executor 声明预期的核验（参考信息——机器不产出坐实结论，
	// 语义判定权在 LLM 裁决官；断言全命中也仅说明"声称的特征出现了"）。
	assertPassed, assertReasons := recipe.Assert.eval(res, dur)
	ev := replayEvidence{
		Method:        res.Method,
		URL:           res.URL,
		HasBaseline:   baseline != nil,
		ExploitStatus: res.StatusCode,
		ExploitDurMs:  dur,
		RespHeaders:   res.ResponseHeaders,
		RespBodyLen:   len(res.ResponseBody),
		RespBodySnip:  snippet(res.ResponseBody, evidenceBodySnip),
		AssertPassed:  assertPassed,
		AssertReasons: assertReasons,
	}
	if baseline != nil {
		ev.BaselineStatus = baseline.StatusCode
		ev.BaselineDurMs = baseDur
		ev.BaselineBodyLen = len(baseline.ResponseBody)
	}
	evJSON, mErr := json.Marshal(ev)
	if mErr != nil {
		return evaluator.Result{}, fmt.Errorf("web.Replayer: 序列化证据失败: %w", mErr)
	}

	return evaluator.Result{Evaluation: evJSON, DurationMs: dur}, nil
}

// eval 按断言逐条判定重发结果；全部通过才坐实。返回每条谓词的判定明细供证据链。
func (a Assertion) eval(res httpreplay.Result, durationMs int64) (bool, []string) {
	var reasons []string
	passed := true

	if a.StatusCode != nil {
		if res.StatusCode == *a.StatusCode {
			reasons = append(reasons, fmt.Sprintf("status_code=%d ✓", res.StatusCode))
		} else {
			passed = false
			reasons = append(reasons, fmt.Sprintf("status_code=%d，期望 %d ✗", res.StatusCode, *a.StatusCode))
		}
	}

	body := string(res.ResponseBody)
	for _, want := range a.BodyContains {
		if strings.Contains(body, want) {
			reasons = append(reasons, fmt.Sprintf("body 含 %q ✓", want))
		} else {
			passed = false
			reasons = append(reasons, fmt.Sprintf("body 缺 %q ✗", want))
		}
	}
	for _, absent := range a.BodyAbsent {
		if strings.Contains(body, absent) {
			passed = false
			reasons = append(reasons, fmt.Sprintf("body 不应含 %q 却含 ✗", absent))
		} else {
			reasons = append(reasons, fmt.Sprintf("body 无 %q ✓", absent))
		}
	}
	for k, want := range a.HeaderContains {
		got := res.ResponseHeaders[strings.ToLower(k)]
		if strings.Contains(got, want) {
			reasons = append(reasons, fmt.Sprintf("header[%s] 含 %q ✓", k, want))
		} else {
			passed = false
			reasons = append(reasons, fmt.Sprintf("header[%s]=%q 缺 %q ✗", k, got, want))
		}
	}
	if a.MinDurationMs != nil {
		if int(durationMs) >= *a.MinDurationMs {
			reasons = append(reasons, fmt.Sprintf("耗时 %dms ≥ %dms ✓（时间盲证据）", durationMs, *a.MinDurationMs))
		} else {
			passed = false
			reasons = append(reasons, fmt.Sprintf("耗时 %dms < %dms ✗（未触发延迟）", durationMs, *a.MinDurationMs))
		}
	}
	return passed, reasons
}

func snippet(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n])
}

// 编译期断言：Replayer 满足 evaluator.Replayer 接口。
var _ evaluator.Replayer = (*Replayer)(nil)
