package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/V3teran/liusha/internal/evaluator"
	"github.com/V3teran/liusha/internal/httpreplay"
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
// 机器可判的坐实配方：完整攻击请求 + 断言，可选良性基线做差分对照。
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
// （形状归各域——加新域晋升门零改动）。write_observation 写入时归一化成此结构。
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
type Replayer struct{}

// NewReplayer 构造域分发 Replayer（零依赖：配方自包含）。
func NewReplayer() *Replayer { return &Replayer{} }

// replayEvidence 是落进 exploration_verification 的复现证据（基线/攻击双对照 + 断言明细）。
type replayEvidence struct {
	Method string `json:"method"`
	URL    string `json:"url"`
	// 基线（配方提供 baseline 时的良性对照）与攻击（配方 request 原样重发）的双对照——
	// 裁决官据此看差分。
	HasBaseline     bool              `json:"has_baseline"`
	BaselineStatus  int               `json:"baseline_status_code,omitempty"`
	BaselineDurMs   int64             `json:"baseline_duration_ms,omitempty"`
	BaselineBodyLen int               `json:"baseline_body_len,omitempty"`
	AttackStatus    int               `json:"attack_status_code"`
	AttackDurMs     int64             `json:"attack_duration_ms"`
	RespHeaders     map[string]string `json:"response_headers"`
	RespBodyLen     int               `json:"response_body_len"`
	RespBodySnip    string            `json:"response_body_snippet"`
	AssertPassed    bool              `json:"assert_passed"`
	AssertReasons   []string          `json:"assert_reasons"` // 每条谓词的判定（通过/失败原因）
}

const evidenceBodySnip = 2048 // 证据里响应体截断长度

// Replay 实现 evaluator.Replayer：按域信封分发。
// web=机器确定性重放采证；generic=配方回显（裁决官自主执行）；无信封=legacy web 嗅探。
func (r *Replayer) Replay(ctx context.Context, primitives json.RawMessage) (evaluator.Result, error) {
	var env ReproEnvelope
	if err := json.Unmarshal(primitives, &env); err != nil {
		return evaluator.Result{}, fmt.Errorf("Replayer: 解析域信封失败: %w", err)
	}
	switch env.Domain {
	case DomainWeb:
		return replayWebEnvelope(ctx, env)
	case DomainGeneric:
		return replayGeneric(env)
	case "":
		return replayLegacyWeb(ctx, primitives)
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

// replayLegacyWeb 历史格式兼容（顶层 request/assert、无信封）：按 web 配方处理。
// 仅服务存量数据；write_observation 写入时已归一化成信封。
func replayLegacyWeb(ctx context.Context, primitives json.RawMessage) (evaluator.Result, error) {
	var probe struct {
		TrafficID     *int64          `json:"traffic_id"`
		Modifications json.RawMessage `json:"modifications"`
		Request       json.RawMessage `json:"request"`
	}
	if json.Unmarshal(primitives, &probe) == nil {
		if probe.TrafficID != nil || len(probe.Modifications) > 0 {
			return evaluator.Result{}, fmt.Errorf("web.Replayer: 旧格式配方（traffic_id/modifications）已废弃——请提供域信封 {\"domain\":\"web\",\"recipe\":{\"request\":...},\"assert\":{...}}")
		}
		if len(probe.Request) == 0 {
			return evaluator.Result{}, fmt.Errorf("Replayer: 缺少 domain 信封（{\"domain\": \"web\"|\"generic\", \"recipe\": ..., \"assert\": ...}）")
		}
	}
	var recipe ReplayRecipe
	if err := json.Unmarshal(primitives, &recipe); err != nil {
		return evaluator.Result{}, fmt.Errorf("web.Replayer: 解析复现配方失败: %w", err)
	}
	return replayWeb(ctx, recipe)
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

// commonSuccessCodes 是页面常态状态码：无 baseline 时仅凭它们不构成漏洞证据
// （有 baseline 时由运行时差分护栏判定，不在此拦）。
var commonSuccessCodes = map[int]bool{200: true, 301: true, 302: true, 304: true}

// assertDiscriminative 闸门鉴别力守卫（假阳性第一道机器防线）：
//   - body_contains 每个子串须 ≥ minBodyContainsLen 个字符（修剪后按字节计）；
//   - 自指断言拒绝：子串是请求 URL/host/path 的组成部分（请求登录页断言含 "login.php"
//     ——正常响应必命中，坐实即假阳性，e2e 实测）；
//   - 无 baseline 时，断言须含至少一个强谓词（body_contains/header_contains/min_duration_ms）
//     或非常态状态码——裸 {status_code:200} 这类页面常态断言直接拒绝。
func assertDiscriminative(a Assertion, attackSrc httpreplay.Source, hasBaseline bool) error {
	for _, want := range a.BodyContains {
		if len(strings.TrimSpace(want)) < minBodyContainsLen {
			return fmt.Errorf("无鉴别力断言——body_contains 子串 %q 短于 %d 字符（几乎必命中任意页面）。断言应捕捉攻击响应独有特征：报错回显/泄露数据的完整子串（如 SQL 错误片段、dump 出的字段值）", want, minBodyContainsLen)
		}
		// 自指检测：断言特征已内嵌在请求里（URL/path/host）——正常响应也必然包含它。
		if strings.Contains(strings.ToLower(attackSrc.URL), strings.ToLower(strings.TrimSpace(want))) {
			return fmt.Errorf("无鉴别力断言——body_contains 子串 %q 就是请求 URL 的组成部分（自指：正常响应也必命中）。断言应捕捉攻击响应独有特征，而不是页面本来就有的内容", want)
		}
	}
	if hasBaseline {
		return nil // 有良性对照：鉴别力由运行时基线差分护栏判定
	}
	strong := len(a.BodyContains) > 0 || len(a.HeaderContains) > 0 || a.MinDurationMs != nil
	if !strong && a.StatusCode != nil && !commonSuccessCodes[*a.StatusCode] {
		strong = true // 非常态状态码（401/403/500…）本身可为信号
	}
	if !strong {
		return fmt.Errorf("无鉴别力断言——无 baseline 时至少需要一个强谓词（body_contains/header_contains/min_duration_ms，或非 200/301/302/304 的 status_code）。页面常态断言（裸 status_code=200 类）命中不构成漏洞证据；建议附 baseline 良性对照")
	}
	return nil
}

// replayWeb web 域机器复放：结构校验 → 鉴别力守卫 → resolve 注入 → baseline 差分护栏 → 攻击重发 → 采证。
func replayWeb(ctx context.Context, recipe ReplayRecipe) (evaluator.Result, error) {
	// 空断言不可坐实：机器无从判定即无法晋升（拒绝橡皮图章）。
	if recipe.Assert.empty() {
		return evaluator.Result{}, fmt.Errorf("web.Replayer: assert 为空，无坐实谓词（至少给一条 status_code/body_contains/body_not_contains/header_contains/min_duration_ms）")
	}
	// 结构先行（request 形状），语义随后（断言鉴别力）——错误信息指向首个真问题。
	attackSrc, err := recipe.Request.toSource()
	if err != nil {
		return evaluator.Result{}, fmt.Errorf("web.Replayer: request 非法——%w", err)
	}
	if dErr := assertDiscriminative(recipe.Assert, attackSrc, recipe.Baseline != nil); dErr != nil {
		return evaluator.Result{}, fmt.Errorf("web.Replayer: %w", dErr)
	}

	// 准备请求：抽服务器现造值注入主请求。抽值失败硬错误，绝不静默 pass（护栏2）。
	if recipe.Resolve != nil {
		name, val, rErr := runResolve(ctx, *recipe.Resolve)
		if rErr != nil {
			return evaluator.Result{}, rErr
		}
		injected := injectResolved(recipe.Request, name, val)
		attackSrc, err = injected.toSource()
		if err != nil {
			return evaluator.Result{}, fmt.Errorf("web.Replayer: resolve 注入后 request 非法——%w", err)
		}
	}

	// 差分复现铁律（业界基线对照法）：配方提供 baseline 时先放良性基线，
	// 断言在基线上也全命中 = 断言无鉴别力（正常响应即满足），拒绝坐实。
	// 坐实的唯一形态：攻击响应呈现基线没有的特征（报错回显/泄露数据/延迟）。
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
		if basePassed, _ := recipe.Assert.eval(bres, baseDur); basePassed {
			return evaluator.Result{}, fmt.Errorf("web.Replayer: 无鉴别力断言——基线（良性请求）同样满足全部谓词，命中不构成漏洞证据（断言应捕捉攻击响应独有特征：报错回显/数据泄露/延迟）")
		}
	}

	// 攻击：配方 request 原样重发。
	start := time.Now()
	res, err := httpreplay.Replay(ctx, attackSrc, httpreplay.Mods{})
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
		AttackStatus:  res.StatusCode,
		AttackDurMs:   dur,
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
