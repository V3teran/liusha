package web

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/V3teran/liusha/internal/httpreplay"
	"github.com/V3teran/liusha/internal/verifier"
)

// TrafficSource 按 id 取已限定 scope 的源流量，投影成 httpreplay.Source。
// 收窄依赖：验证层只认 TrafficSource 叶子契约，不依赖任何具体 store 实现。不存在/越界返回 (_, false, nil)。
type TrafficSource interface {
	GetInScope(ctx context.Context, id int64) (httpreplay.Source, bool, error)
}

// ReplayRecipe 是 web 域的 L1 复现原语（verifier.Attempt.Primitives 的形状）。
// 机器可判的坐实配方：拿哪条源流量、怎么改写、拿什么断言判坐实。
// Resolve 非空时，主 replay 前先发一个准备请求抽新鲜值注入——专治 replay 时
// 无法从源流量复用的服务器现造值（opaque-id / nonce / 过期 token）。
type ReplayRecipe struct {
	Resolve       *ResolveStep    `json:"resolve,omitempty"`
	TrafficID     int64           `json:"traffic_id"`
	Modifications httpreplay.Mods `json:"modifications"`
	Assert        Assertion       `json:"assert"`
}

// Assertion 是坐实断言：全部满足才算复现坐实。至少要有一条谓词，
// 否则是橡皮图章（空断言"确认"一切）——拒绝坐实。
type Assertion struct {
	StatusCode     *int              `json:"status_code,omitempty"`     // 响应码须等于此值
	BodyContains   []string          `json:"body_contains,omitempty"`   // 响应体须含全部子串（越权拿到数据、SQL 报错、XSS 回显）
	BodyAbsent     []string          `json:"body_absent,omitempty"`     // 响应体须不含任一子串（未跳登录页=认证绕过坐实）
	HeaderContains map[string]string `json:"header_contains,omitempty"` // 响应头 key→子串
}

func (a Assertion) empty() bool {
	return a.StatusCode == nil && len(a.BodyContains) == 0 &&
		len(a.BodyAbsent) == 0 && len(a.HeaderContains) == 0
}

// Replayer 是 web 域的 verifier.Replayer：重发源流量的改写版，按断言判是否坐实。
type Replayer struct {
	traffic TrafficSource
}

// NewReplayer 构造 web Replayer。traffic 为 nil 时 Replay 会报错。
func NewReplayer(traffic TrafficSource) *Replayer {
	return &Replayer{traffic: traffic}
}

// replayEvidence 是落进 wm_verification 的复现证据（req/resp + 断言判定明细）。
type replayEvidence struct {
	TrafficID     int64             `json:"traffic_id"`
	Method        string            `json:"method"`
	URL           string            `json:"url"`
	StatusCode    int               `json:"status_code"`
	RespHeaders   map[string]string `json:"response_headers"`
	RespBodyLen   int               `json:"response_body_len"`
	RespBodySnip  string            `json:"response_body_snippet"`
	AssertPassed  bool              `json:"assert_passed"`
	AssertReasons []string          `json:"assert_reasons"` // 每条谓词的判定（通过/失败原因）
}

const evidenceBodySnip = 2048 // 证据里响应体截断长度

// Replay 实现 verifier.Replayer：解析 recipe → 取源流量 → httpreplay 重发 → 断言判坐实。
func (r *Replayer) Replay(ctx context.Context, primitives json.RawMessage) (verifier.Result, error) {
	if r.traffic == nil {
		return verifier.Result{}, fmt.Errorf("web.Replayer: 无 TrafficSource，无法复现")
	}

	var recipe ReplayRecipe
	if err := json.Unmarshal(primitives, &recipe); err != nil {
		return verifier.Result{}, fmt.Errorf("web.Replayer: 解析复现配方失败: %w", err)
	}
	if recipe.TrafficID <= 0 {
		return verifier.Result{}, fmt.Errorf("web.Replayer: traffic_id 必填且 > 0")
	}
	// 空断言不可坐实：机器无从判定即无法晋升（拒绝橡皮图章）。
	if recipe.Assert.empty() {
		return verifier.Result{}, fmt.Errorf("web.Replayer: assert 为空，无坐实谓词（至少给一条 status_code/body_contains/body_absent/header_contains）")
	}

	// 准备请求：抽服务器现造值注入主请求。抽值失败硬错误，绝不静默 pass（护栏2）。
	if recipe.Resolve != nil {
		name, val, err := r.runResolve(ctx, *recipe.Resolve)
		if err != nil {
			return verifier.Result{}, err
		}
		recipe.Modifications = injectResolved(recipe.Modifications, name, val)
	}

	src, ok, err := r.traffic.GetInScope(ctx, recipe.TrafficID)
	if err != nil {
		return verifier.Result{}, fmt.Errorf("web.Replayer: 读源流量 %d 失败: %w", recipe.TrafficID, err)
	}
	if !ok {
		return verifier.Result{}, fmt.Errorf("web.Replayer: 源流量 %d 不存在或越界", recipe.TrafficID)
	}

	start := time.Now()
	res, err := httpreplay.Replay(ctx, src, recipe.Modifications)
	dur := time.Since(start).Milliseconds()
	if err != nil {
		return verifier.Result{}, fmt.Errorf("web.Replayer: 重发失败: %w", err)
	}

	passed, reasons := recipe.Assert.eval(res)
	ev := replayEvidence{
		TrafficID:     recipe.TrafficID,
		Method:        res.Method,
		URL:           res.URL,
		StatusCode:    res.StatusCode,
		RespHeaders:   res.ResponseHeaders,
		RespBodyLen:   len(res.ResponseBody),
		RespBodySnip:  snippet(res.ResponseBody, evidenceBodySnip),
		AssertPassed:  passed,
		AssertReasons: reasons,
	}
	evJSON, _ := json.Marshal(ev)

	return verifier.Result{Confirmed: passed, Evidence: evJSON, DurationMs: dur}, nil
}

// runResolve 发准备请求并抽新鲜值，返回 (占位名, 值)。任一步失败都硬错误。
func (r *Replayer) runResolve(ctx context.Context, step ResolveStep) (string, string, error) {
	if step.TrafficID <= 0 {
		return "", "", fmt.Errorf("web.Replayer: resolve.traffic_id 必填且 > 0")
	}
	src, ok, err := r.traffic.GetInScope(ctx, step.TrafficID)
	if err != nil {
		return "", "", fmt.Errorf("web.Replayer: 读准备流量 %d 失败: %w", step.TrafficID, err)
	}
	if !ok {
		return "", "", fmt.Errorf("web.Replayer: 准备流量 %d 不存在或越界", step.TrafficID)
	}
	res, err := httpreplay.Replay(ctx, src, step.Modifications)
	if err != nil {
		return "", "", fmt.Errorf("web.Replayer: 准备请求重发失败: %w", err)
	}
	val, err := step.Extract.extract(res)
	if err != nil {
		return "", "", fmt.Errorf("web.Replayer: %w", err)
	}
	return step.Extract.Name, val, nil
}

// eval 按断言逐条判定重发结果；全部通过才坐实。返回每条谓词的判定明细供证据链。
func (a Assertion) eval(res httpreplay.Result) (bool, []string) {
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
	return passed, reasons
}

func snippet(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n])
}

// 编译期断言：Replayer 满足 verifier.Replayer 接口。
var _ verifier.Replayer = (*Replayer)(nil)
