package httprepro

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/evaluator"
	"github.com/V3teran/liusha/internal/httpreplay"
	"github.com/V3teran/liusha/internal/repro"
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
		return httpreplay.Source{}, invalidRecipe("url 必须是完整 URL（含 http:// 或 https://），当前 %q", r.URL)
	}
	if _, err := url.Parse(r.URL); err != nil {
		return httpreplay.Source{}, invalidRecipe("url 解析失败: %v", err)
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

// ReplayRecipe 是 http 配方的 recipe 段（evaluator.Claim.Repro 信封里的形状）。
// 机器可判的坐实配方：完整验证请求 + 断言，可选对照请求做差分。
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
type Assertion struct {
	StatusCode      *int     `json:"status_code,omitempty"`       // 响应码须等于此值
	BodyContains    []string `json:"body_contains,omitempty"`     // 响应体须含全部子串
	BodyNotContains []string `json:"body_not_contains,omitempty"` // 响应体须不含任一子串
	// 响应头 key→子串：头须存在且含子串（子串为空即只要求头存在）。
	HeaderContains map[string]string `json:"header_contains,omitempty"`
	// 响应头 key→子串：头不存在，或存在但不含子串（子串为空即要求头不存在）——
	// 缺失安全头、Cookie 缺 HttpOnly 这类配置弱点的证据。
	HeaderNotContains map[string]string `json:"header_not_contains,omitempty"`
	MinDurationMs     *int              `json:"min_duration_ms,omitempty"` // 响应耗时至少 N ms
}

func (a Assertion) empty() bool {
	return a.StatusCode == nil && len(a.BodyContains) == 0 && len(a.BodyNotContains) == 0 &&
		len(a.HeaderContains) == 0 && len(a.HeaderNotContains) == 0 && a.MinDurationMs == nil
}

// invalidRecipe 包装确定性配方缺陷（形状不对、缺字段）：重放必然同样失败，复现门据
// evaluator.ErrInvalidRecipe 快速终局出圈。
func invalidRecipe(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{evaluator.ErrInvalidRecipe}, args...)...)
}

// Replayer 是 http 配方的机器复现通道（evaluator.Replayer）：确定性重发配方里的完整
// HTTP 请求并按断言采证。不依赖任何流量存储——配方自包含（工具自发流量同样可复现）。
type Replayer struct {
	creds credential.Provider // 可选：按 host 注入会话上下文（配方缺 Cookie 时补全）
	host  string
}

// NewReplayer 构造 http 配方的复现通道（零依赖：配方自包含）。
func NewReplayer() *Replayer { return &Replayer{} }

// WithCredentials 注入凭据提供方与目标 host：重放请求缺会话头
// （Cookie/Authorization）时，用凭据池里该 host 的首个身份补全——
// 配方自包含是理想，LLM 漏写会话上下文时重放会打到登录页导致真实主张
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
	// 临时会话（浏览器快照里停在另一种应用状态的会话）注入复放，让全部假设在
	// 错误的应用状态下整体证伪。持久身份是显式录入的权威凭据；自动会话快照只在
	// 无持久身份时兜底。
	sort.SliceStable(identities, func(i, j int) bool {
		pi, pj := credential.IsSessionIdentity(identities[i].Username), credential.IsSessionIdentity(identities[j].Username)
		if pi != pj {
			return !pi // 持久在前
		}
		return identities[i].Username < identities[j].Username
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

// sessionRefreshAttempt 是一次会话刷新重试的审计记录（落进证据链，
// 评估者可据此区分「环境会话过期」与「主张不成立」）。
type sessionRefreshAttempt struct {
	Source       string `json:"source"`        // 凭据身份名（池内 label）
	StatusCode   int    `json:"status_code"`   // 刷新后验证响应码
	AssertPassed bool   `json:"assert_passed"` // 刷新后断言是否全过
	Note         string `json:"note,omitempty"`
}

// replayEvidence 是落进 exploration_verification 的复现证据（对照/验证请求双对照 + 断言明细）。
type replayEvidence struct {
	Method string `json:"method"`
	URL    string `json:"url"`
	// 对照请求（配方提供 baseline 时）与验证请求（配方 request 原样重发）的双对照——
	// 评估者据此看差分。
	HasBaseline     bool              `json:"has_baseline"`
	BaselineStatus  int               `json:"baseline_status_code,omitempty"`
	BaselineDurMs   int64             `json:"baseline_duration_ms,omitempty"`
	BaselineBodyLen int               `json:"baseline_body_len,omitempty"`
	StatusCode      int               `json:"status_code"`
	DurationMs      int64             `json:"duration_ms"`
	RespHeaders     map[string]string `json:"response_headers"`
	RespBodyLen     int               `json:"response_body_len"`
	RespBodySnip    string            `json:"response_body_snippet"`
	AssertPassed    bool              `json:"assert_passed"`
	AssertReasons   []string          `json:"assert_reasons"` // 每条谓词的判定（通过/失败原因）
	// SessionRefresh 记录认证弹跳触发的池凭据替换重试（首轮无弹跳则为空）。
	SessionRefresh []sessionRefreshAttempt `json:"session_refresh,omitempty"`
}

const evidenceBodySnip = 2048 // 证据里响应体截断长度

// Replay 实现 evaluator.Replayer：重放 http 配方并采证。目标侧不可达（没拿到
// 响应）包装成 evaluator.ErrTargetUnreachable，复现门据此计入重试阶梯。
func (r *Replayer) Replay(ctx context.Context, envelope json.RawMessage) (evaluator.Result, error) {
	var env repro.Envelope
	if err := json.Unmarshal(envelope, &env); err != nil {
		return evaluator.Result{}, invalidRecipe("解析配方信封失败: %v", err)
	}
	if env.Kind != Name {
		return evaluator.Result{}, invalidRecipe("http 复现通道收到 %q 形状的配方", env.Kind)
	}
	env.Recipe = r.injectSessionContext(ctx, env.Recipe)
	res, err := r.replayEnvelope(ctx, env)
	if errors.Is(err, httpreplay.ErrTransport) {
		return res, fmt.Errorf("%w: %w", evaluator.ErrTargetUnreachable, err)
	}
	return res, err
}

// sessionHeaderKeys 是会话类请求头的小写键集——刷新重试只替换这些键，
// 其余头（Content-Type / 自定义头等）原样保留。
var sessionHeaderKeys = map[string]bool{
	"cookie":        true,
	"authorization": true,
}

// loginLocationRe 匹配重定向 Location 里的认证语义路径段。
var loginLocationRe = regexp.MustCompile(`(?i)(login|log-?on|sign-?in|auth|sso)`)

// passwordInputRe 匹配响应体里的密码输入框（登录页形态信号）。
var passwordInputRe = regexp.MustCompile(`(?i)<input[^>]+type=["']?password`)

// loginPathRe 匹配请求 URL 本身的登录页路径——配方目标就是登录页时
// （登录绕过类假设），password 表单是预期形态，不构成认证弹跳。
var loginPathRe = regexp.MustCompile(`(?i)/(login|log-?on|sign-?in|auth)`)

// authBounced 判定一次重放响应是否呈认证弹跳形态（会话失效的信号）：
// 401；3xx 且 Location 指向登录/认证路径；或响应体含密码输入框而配方目标
// 本身不是登录页。保守集合——403 不算（访问控制类假设里 403 是待检信号而非弹跳）。
func authBounced(res httpreplay.Result) bool {
	if res.StatusCode == 401 {
		return true
	}
	if res.StatusCode >= 301 && res.StatusCode <= 308 {
		if loc := res.ResponseHeaders["location"]; loc != "" && loginLocationRe.MatchString(loc) {
			return true
		}
	}
	if !loginPathRe.MatchString(res.URL) && passwordInputRe.Match(res.ResponseBody) {
		return true
	}
	return false
}

// sessionRefreshCandidates 返回池内可用于刷新重试的凭据身份（有序、有界）：
// 持久身份优先于自动会话快照（快照可能停在中间状态），名称破平保证确定性。
func (r *Replayer) sessionRefreshCandidates(ctx context.Context) []struct {
	label   string
	headers map[string]string
} {
	if r.creds == nil {
		return nil
	}
	identities, err := r.creds.GetIdentitiesByHost(ctx, r.host)
	if err != nil {
		return nil
	}
	sort.SliceStable(identities, func(i, j int) bool {
		pi, pj := credential.IsSessionIdentity(identities[i].Username), credential.IsSessionIdentity(identities[j].Username)
		if pi != pj {
			return !pi // 持久在前
		}
		return identities[i].Username < identities[j].Username
	})
	const maxCandidates = 3
	out := make([]struct {
		label   string
		headers map[string]string
	}, 0, maxCandidates)
	for _, id := range identities {
		headers := map[string]string{}
		for _, c := range id.Credentials {
			if c.Type != credential.TypeHeaders || c.Key == "" || c.Value == "" {
				continue
			}
			headers[strings.ToLower(c.Key)] = c.Value
		}
		if len(headers) == 0 {
			continue
		}
		out = append(out, struct {
			label   string
			headers map[string]string
		}{label: id.Username, headers: headers})
		if len(out) >= maxCandidates {
			break
		}
	}
	return out
}

// replaceSessionHeaders 把 recipe（request/baseline）里的会话类头整体替换为
// 候选身份的头值（其余头保留）。返回替换后的 recipe JSON。
func replaceSessionHeaders(recipe json.RawMessage, headers map[string]string) json.RawMessage {
	var rc struct {
		Request  json.RawMessage `json:"request"`
		Baseline json.RawMessage `json:"baseline"`
	}
	if json.Unmarshal(recipe, &rc) != nil || len(rc.Request) == 0 {
		return recipe
	}
	apply := func(req json.RawMessage) (json.RawMessage, bool) {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(req, &m); err != nil || m == nil {
			return req, false
		}
		var existing map[string]string
		if h, ok := m["headers"]; ok && len(h) > 0 {
			_ = json.Unmarshal(h, &existing)
		}
		changed := false
		for k := range existing {
			if sessionHeaderKeys[strings.ToLower(k)] {
				delete(existing, k)
				changed = true
			}
		}
		for k, v := range headers {
			if sessionHeaderKeys[k] {
				existing[k] = v
				changed = true
			}
		}
		if !changed {
			return req, false
		}
		hb, err := json.Marshal(existing)
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
	reqOK, reqChanged := apply(rc.Request)
	if reqChanged {
		rc.Request = reqOK
		changedAny = true
	}
	if len(rc.Baseline) > 0 {
		if bOK, bChanged := apply(rc.Baseline); bChanged {
			rc.Baseline = bOK
			changedAny = true
		}
	}
	if !changedAny {
		return recipe // 无会话键可替换——原样返回，不加无谓重放轮次
	}
	out, err := json.Marshal(rc)
	if err != nil {
		return recipe
	}
	return out
}

// replayEnvelope 信封化 http 配方并重放：结构校验 → 首轮重放 →
// 认证弹跳时按池凭据刷新会话重试（会话新鲜度守卫）。
//
// 守卫动因：配方是执行时刻的冻结快照，内嵌的会话材料（PHPSESSID 等）在
// 验证时刻可能已被服务端轮换失效——「配方显式头优先」的补缺策略会让过期
// 凭据赢过池内新鲜会话，重放弹回登录页、真实主张被误证伪。首轮证据若呈
// 认证弹跳形态，依次用池内身份替换会话头重放；任一轮断言通过即以该轮
// 证据返回，全部尝试记入证据链（评估者可分辨环境失效与主张不成立）。
func (r *Replayer) replayEnvelope(ctx context.Context, env repro.Envelope) (evaluator.Result, error) {
	if len(env.Recipe) == 0 {
		return evaluator.Result{}, invalidRecipe("recipe 为空（需 {\"request\": {...}, \"baseline\": {...}?}）")
	}
	var core struct {
		Request  SelfContainedRequest  `json:"request"`
		Baseline *SelfContainedRequest `json:"baseline"`
	}
	if err := json.Unmarshal(env.Recipe, &core); err != nil {
		return evaluator.Result{}, invalidRecipe("解析 recipe 失败: %v", err)
	}
	var assert Assertion
	if len(env.Assert) == 0 {
		return evaluator.Result{}, invalidRecipe("assert 为空，无坐实谓词（至少给一条 status_code/body_contains/body_not_contains/header_contains/header_not_contains/min_duration_ms）")
	}
	if err := json.Unmarshal(env.Assert, &assert); err != nil {
		return evaluator.Result{}, invalidRecipe("解析 assert 失败: %v", err)
	}

	// 首轮重放（补缺后的配方原样）
	recipe := ReplayRecipe{Request: core.Request, Baseline: core.Baseline, Assert: assert}
	ev, lastRes, err := r.replayOnce(ctx, recipe)
	if err != nil {
		return evaluator.Result{}, err
	}

	// 会话新鲜度守卫：断言失败 + 认证弹跳 → 池凭据替换重试。
	if !ev.AssertPassed && authBounced(lastRes) {
		for _, cand := range r.sessionRefreshCandidates(ctx) {
			refreshedRaw := replaceSessionHeaders(env.Recipe, cand.headers)
			var refreshedCore struct {
				Request  SelfContainedRequest  `json:"request"`
				Baseline *SelfContainedRequest `json:"baseline"`
			}
			if json.Unmarshal(refreshedRaw, &refreshedCore) != nil {
				continue
			}
			attempt := sessionRefreshAttempt{Source: cand.label}
			ev2, _, err2 := r.replayOnce(ctx, ReplayRecipe{
				Request: refreshedCore.Request, Baseline: refreshedCore.Baseline, Assert: assert,
			})
			if err2 != nil {
				attempt.Note = err2.Error()
				ev.SessionRefresh = append(ev.SessionRefresh, attempt)
				continue
			}
			attempt.StatusCode = ev2.StatusCode
			attempt.AssertPassed = ev2.AssertPassed
			ev.SessionRefresh = append(ev.SessionRefresh, attempt)
			if ev2.AssertPassed {
				ev2.SessionRefresh = ev.SessionRefresh
				return marshalEvidence(ev2, ev2.DurationMs)
			}
		}
	}

	return marshalEvidence(ev, ev.DurationMs)
}

// marshalEvidence 序列化证据为 evaluator.Result。
func marshalEvidence(ev replayEvidence, durMs int64) (evaluator.Result, error) {
	evJSON, mErr := json.Marshal(ev)
	if mErr != nil {
		return evaluator.Result{}, fmt.Errorf("http 复现: 序列化证据失败: %w", mErr)
	}
	return evaluator.Result{Evidence: evJSON, DurationMs: durMs}, nil
}

// assertDiscriminative 闸门鉴别力守卫（假阳性第一道机器防线）：
//   - body_contains 每个子串须 ≥ MinBodyContainsLen 个字符（修剪后按字节计）
//
// 自指与「无 baseline 须有强谓词」不在机器侧判定，交 LLM 评估者终判；写入侧
// （Kind.Normalize）与本守卫同一口径。
func assertDiscriminative(a Assertion) error {
	for _, want := range a.BodyContains {
		if len(strings.TrimSpace(want)) < MinBodyContainsLen {
			return fmt.Errorf("%w: body_contains 子串 %q 短于 %d 字符（几乎必命中任意页面）。断言应捕捉验证响应独有的特征：只在主张成立时才出现的完整内容片段", evaluator.ErrAssertNotDiscriminative, want, MinBodyContainsLen)
		}
	}
	return nil
}

// replayOnce 单轮机器复放：结构校验 → 鉴别力守卫 → resolve 注入
// → baseline 差分对照 → 验证请求重发 → 采证。返回证据与验证响应（供调用方
// 做认证弹跳检测）。机器不产出坐实结论——语义判定权在 LLM 评估者。
func (r *Replayer) replayOnce(ctx context.Context, recipe ReplayRecipe) (replayEvidence, httpreplay.Result, error) {
	var ev replayEvidence
	// 空断言不可坐实：机器无从判定即无法晋升（拒绝橡皮图章）。
	if recipe.Assert.empty() {
		return ev, httpreplay.Result{}, invalidRecipe("assert 为空，无坐实谓词（至少给一条 status_code/body_contains/body_not_contains/header_contains/header_not_contains/min_duration_ms）")
	}
	// 结构先行（request 形状），语义随后（断言鉴别力）——错误信息指向首个真问题。
	requestSrc, err := recipe.Request.toSource()
	if err != nil {
		return ev, httpreplay.Result{}, fmt.Errorf("http 复现: request 非法——%w", err)
	}
	if dErr := assertDiscriminative(recipe.Assert); dErr != nil {
		return ev, httpreplay.Result{}, fmt.Errorf("http 复现: %w", dErr)
	}

	// 准备请求：抽服务器现造值注入主请求。抽值失败硬错误，绝不静默 pass（护栏2）。
	if recipe.Resolve != nil {
		name, val, rErr := runResolve(ctx, *recipe.Resolve)
		if rErr != nil {
			return ev, httpreplay.Result{}, rErr
		}
		injected := injectResolved(recipe.Request, name, val)
		requestSrc, err = injected.toSource()
		if err != nil {
			return ev, httpreplay.Result{}, fmt.Errorf("http 复现: resolve 注入后 request 非法——%w", err)
		}
	}

	// 差分复现（业界基线对照法）：配方提供 baseline 时先放良性基线——
	// 断言在基线上也全命中 = 断言无鉴别力。机器护栏不在此提前证伪
	// （执行器断言质量不稳定，差分语义终判交 LLM 评估者，其可见完整双对照）。
	var baseline *httpreplay.Result
	var baseDur int64
	if recipe.Baseline != nil {
		baseSrc, bErr := recipe.Baseline.toSource()
		if bErr != nil {
			return ev, httpreplay.Result{}, fmt.Errorf("http 复现: baseline 非法——%w", bErr)
		}
		baseStart := time.Now()
		bres, bErr := httpreplay.Replay(ctx, baseSrc, httpreplay.Overrides{})
		baseDur = time.Since(baseStart).Milliseconds()
		if bErr != nil {
			return ev, httpreplay.Result{}, fmt.Errorf("http 复现: 基线重放失败: %w", bErr)
		}
		baseline = &bres
	}

	// 验证请求：配方 request 原样重发。
	start := time.Now()
	res, err := httpreplay.Replay(ctx, requestSrc, httpreplay.Overrides{})
	dur := time.Since(start).Milliseconds()
	if err != nil {
		return ev, httpreplay.Result{}, fmt.Errorf("http 复现: 重发失败: %w", err)
	}

	// assert 命中明细：executor 声明预期的核验（参考信息——机器不产出坐实结论，
	// 语义判定权在 LLM 评估者；断言全命中也仅说明"声称的特征出现了"）。
	assertPassed, assertReasons := recipe.Assert.eval(res, dur)
	ev = replayEvidence{
		Method:        res.Method,
		URL:           res.URL,
		HasBaseline:   baseline != nil,
		StatusCode:    res.StatusCode,
		DurationMs:    dur,
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

	return ev, res, nil
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
	for _, absent := range a.BodyNotContains {
		if strings.Contains(body, absent) {
			passed = false
			reasons = append(reasons, fmt.Sprintf("body 不应含 %q 却含 ✗", absent))
		} else {
			reasons = append(reasons, fmt.Sprintf("body 无 %q ✓", absent))
		}
	}
	for k, want := range a.HeaderContains {
		got, present := res.ResponseHeaders[strings.ToLower(k)]
		switch {
		case !present:
			passed = false
			reasons = append(reasons, fmt.Sprintf("header[%s] 不存在 ✗", k))
		case strings.Contains(got, want):
			reasons = append(reasons, fmt.Sprintf("header[%s] 含 %q ✓", k, want))
		default:
			passed = false
			reasons = append(reasons, fmt.Sprintf("header[%s]=%q 缺 %q ✗", k, got, want))
		}
	}
	for k, absent := range a.HeaderNotContains {
		got, present := res.ResponseHeaders[strings.ToLower(k)]
		switch {
		case !present:
			reasons = append(reasons, fmt.Sprintf("header[%s] 不存在 ✓", k))
		case absent == "":
			passed = false
			reasons = append(reasons, fmt.Sprintf("header[%s]=%q 应不存在却存在 ✗", k, got))
		case strings.Contains(got, absent):
			passed = false
			reasons = append(reasons, fmt.Sprintf("header[%s]=%q 不应含 %q 却含 ✗", k, got, absent))
		default:
			reasons = append(reasons, fmt.Sprintf("header[%s] 无 %q ✓", k, absent))
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
