// Package llm 的重试装饰器：按错误码退避表自动重试，重试耗尽即失败。
//
// 退避表与错误分类是框架能力，定义在本包；Router 装配 Provider 时包上本装饰器。
// 限流以上游告知的恢复时刻为准（见 retry_at.go）：瞬时限流等完接着发，额度窗口转入
// 冷却（见 cooldown.go），都不按退避表盲等。
package llm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"strings"
	"time"
)

// RetryOptions 控制 4 类错误的重试次数和 backoff schedule。
type RetryOptions struct {
	MaxRetries429 int
	MaxRetries529 int
	MaxRetries5xx int
	MaxRetriesNet int

	Backoff429 []time.Duration // 长度需 ≥ MaxRetries429
	Backoff5xx []time.Duration // 长度需 ≥ MaxRetries5xx
	BackoffNet []time.Duration // 长度需 ≥ MaxRetriesNet
	Backoff529 time.Duration   // 单值（529 只重试 1 次）
}

// DefaultRetryOptions 返回内置退避表。429 档取长尾：并发型限流的恢复窗口以十秒计，
// 短退避只会连撞几次后放弃；四个 Agent 并发调用的场景下，长尾 + 抖动让重试天然错峰。
// 额度型限流（按小时、按天重置）不在这张表的射程内，由额度窗口冷却处理。
func DefaultRetryOptions() RetryOptions {
	return RetryOptions{
		MaxRetries429: 4,
		MaxRetries529: 1,
		MaxRetries5xx: 2,
		MaxRetriesNet: 2,
		Backoff429:    []time.Duration{2 * time.Second, 8 * time.Second, 30 * time.Second, 60 * time.Second},
		Backoff5xx:    []time.Duration{1 * time.Second, 4 * time.Second},
		BackoffNet:    []time.Duration{1 * time.Second, 3 * time.Second},
		Backoff529:    0, // 立即重试
	}
}

// jitterDuration 等量抖动：在 [d/2, d) 均匀取值。固定退避表让并发调用方
// 同相位重撞（第一轮全部 1s 后齐发），抖动把重试在时间窗内摊开。
func jitterDuration(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	half := d / 2
	// #nosec G404 -- 重试时序抖动非安全敏感：只把并发重试在时间窗内摊开，弱随机足够且免错误分支。
	return half + time.Duration(rand.Int64N(int64(half)))
}

// ErrorClass 是 4 类可重试错误的判别结果。
type ErrorClass int

// Class429 等枚举定义。
const (
	// ClassOther 等枚举判别结果：决定走哪条退避策略。
	ClassOther ErrorClass = iota // 不重试（含其他 4xx / 业务错误）
	Class429
	Class529
	Class5xx
	ClassNet
)

// Classify 按优先级解析 error：
//  1. *HTTPError 显式类型直接读 Code
//  2. net.Error.Timeout()
//  3. EOF / connection reset / i/o timeout / DNS 解析失败等网络词
//  4. err.Error() 启发式包含状态码（部分 provider 不在 error 结构里暴露 code）
//
// 不能识别的一律 ClassOther（不重试）。
//
// 本函数是全系统网络瞬时错误的**唯一分类事实源**：planner 的重试判定、
// evaluator 的瞬时故障豁免都经 IsTransient 委托到这里——业务侧不再自维护
// 关键词表（曾因三处词表各自漂移，DNS 抖动被误判为不可重试错误）。
func Classify(err error) ErrorClass {
	if err == nil {
		return ClassOther
	}
	var he *HTTPError
	if errors.As(err, &he) {
		return ClassifyCode(he.Code)
	}
	// 流未见收尾事件即关闭（provider 掉流）：传输中断属瞬时故障，与下面的网络词同档。
	// 只影响 Complete 的整轮重试与调用方阶梯的瞬时判定；Stream 建立后的中途失败
	// 仍不自动重试（已发增量不可收回，决策权在调用方）。
	if errors.Is(err, ErrStreamTruncated) {
		return ClassNet
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return ClassNet
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return ClassNet
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		// 取消不是瞬时故障（调用方主动放弃），重试无意义；
		// 超时是瞬时故障（预算耗尽），换一次机会有意义。
		if errors.Is(err, context.Canceled) {
			return ClassOther
		}
		return ClassNet
	}
	msg := err.Error()
	low := strings.ToLower(msg)
	if strings.Contains(low, "connection reset") ||
		strings.Contains(low, "connection refused") ||
		strings.Contains(low, "connection closed") ||
		strings.Contains(low, "i/o timeout") ||
		strings.Contains(low, "eof") ||
		strings.Contains(low, "dial tcp") ||
		strings.Contains(low, "no such host") ||
		strings.Contains(low, "server misbehaving") ||
		strings.Contains(low, "network unreachable") ||
		strings.Contains(low, "broken pipe") ||
		strings.Contains(low, "tls handshake timeout") {
		return ClassNet
	}
	if strings.Contains(low, "context deadline exceeded") ||
		strings.Contains(low, "timeout") ||
		strings.Contains(low, "deadline exceeded") {
		return ClassNet
	}
	if strings.Contains(msg, "429") {
		return Class429
	}
	if strings.Contains(msg, "529") {
		return Class529
	}
	for _, code := range []string{"500", "502", "503", "504"} {
		if strings.Contains(msg, code) {
			return Class5xx
		}
	}
	return ClassOther
}

// IsTransient 报告错误是否为瞬时故障（网络抖动 / 限流 / 服务端过载 / 超时）——
// 重试有意义。业务侧（planner 重试、evaluator 止损豁免）统一经此判定，
// 不再各自维护关键词表。
func IsTransient(err error) bool {
	return Classify(err) != ClassOther
}

// ErrNotConfigured 标记「provider / 密钥 / 路由未配置」类错误：与部署使用无关、
// 用户提供配置即可消除——重试不会自愈，调用方应把任务干净终结并给出配置指引，
// 而不是轮询重撞（实测：零 provider 下发后任务永挂 active）。Router 与 llmstore
// 的密钥解析统一包此哨兵，IsConfigError 据此判定。
var ErrNotConfigured = errors.New("LLM 未配置")

// ErrUnexpectedResponse 标记上游返回了 2xx 但响应不符合所选协议（OpenAI 兼容没有
// choices、Anthropic 没有消息 id）：多为地址指向了别的协议，或网关把错误包成 200 返回。
// 与 404 同属「这个地址不讲这个协议」，连接诊断据此换候选地址与协议实测。
var ErrUnexpectedResponse = errors.New("响应不符合所选协议")

// IsConfigError 报告错误是否为 provider 配置性错误：鉴权失败（401）、无权限（403）、
// 模型或端点不存在（404）。这类错误与具体请求无关、重试不会自愈，调用方应止损并把
// 原因上报给操作者——与 400（单次请求非法，如上下文超长，换个请求可能成功）区分开。
// 只认结构化 *HTTPError：provider 适配层对非 2xx 统一构造它，不做文本启发式。
func IsConfigError(err error) bool {
	if errors.Is(err, ErrNotConfigured) {
		return true
	}
	var he *HTTPError
	if !errors.As(err, &he) {
		return false
	}
	switch he.Code {
	case 401, 403, 404:
		return true
	}
	return false
}

// ClassifyCode 按 HTTP 状态码分类。
func ClassifyCode(code int) ErrorClass {
	switch code {
	case 429:
		return Class429
	case 529:
		return Class529
	case 500, 502, 503, 504:
		return Class5xx
	}
	return ClassOther // 含 400/401/403/404 等其他 4xx
}

// SleepCtx 是支持 ctx 取消的 sleep。d <= 0 时直接返回 nil（仅检查 ctx）。
func SleepCtx(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// WithRetry 返回带退避重试的 Provider。cd 是进程内共享的额度窗口登记（由 Router 持有：
// 已构造的 Provider 实例会定期重建，登记不能挂在实例上），account 是这份 provider 所用
// 额度的归属标识（见 quotaIdentity）。
func WithRetry(inner Provider, opts RetryOptions, cd *cooldown, account string) Provider {
	return &retryProvider{inner: inner, opts: opts, cooldown: cd, account: account}
}

type retryProvider struct {
	inner    Provider
	opts     RetryOptions
	cooldown *cooldown
	account  string
}

func (r *retryProvider) ModelID() string    { return r.inner.ModelID() }
func (r *retryProvider) ProviderID() string { return r.inner.ProviderID() }

// Complete 重试主循环：按错误类别决定退避重试或放弃。
func (r *retryProvider) Complete(ctx context.Context, req Request) (Response, error) {
	if err := ctx.Err(); err != nil {
		return Response{}, err
	}
	probe, err := r.awaitRecovery(ctx)
	if err != nil {
		return Response{}, err
	}
	resp, err := r.inner.Complete(ctx, req)
	if err == nil {
		if probe {
			r.cooldown.lift(r.account)
		}
		return resp, nil
	}
	switch Classify(err) {
	case ClassOther:
		return Response{}, err
	case Class429:
		if until, ok := r.registerQuotaWindow(err); ok {
			return Response{}, r.hold(ctx, until, err)
		}
		return r.retryLoop(ctx, req, err, r.opts.MaxRetries429, r.opts.Backoff429)
	case Class529:
		return r.retry529(ctx, req, err)
	case Class5xx:
		return r.retryLoop(ctx, req, err, r.opts.MaxRetries5xx, r.opts.Backoff5xx)
	case ClassNet:
		return r.retryLoop(ctx, req, err, r.opts.MaxRetriesNet, r.opts.BackoffNet)
	}
	return Response{}, err
}

// awaitRecovery 在发请求前执行：这份额度若处于窗口内，按登记处的准入结果等待、放行探测或
// 交还。返回这是不是一次放行的探测——探测成功后由调用方解除窗口。
func (r *retryProvider) awaitRecovery(ctx context.Context) (probe bool, err error) {
	decision, e := r.cooldown.admit(r.account, time.Now())
	switch decision {
	case admitFree:
		return false, nil
	case admitProbe:
		return true, nil
	case admitTail:
		// 窗口尾声：等到恢复时刻就发，不必交还外层绕一圈。
		if r.cooldown.holdInWindow && SleepCtx(ctx, stagger(time.Until(e.until))) == nil {
			return false, nil
		}
		// 等待被打断（预算用尽或调用方取消）：ctx 已结束，下面的等待不再占用时间，
		// 错误口径与窗口内一致。
	}
	// 这一次调用没有发出去：标记下来，审计不留提示词（没有发出去的请求没有失败现场）。
	return false, &heldError{err: r.hold(ctx, e.until, e.cause)}
}

// registerQuotaWindow 报告这次限流是否宣告了一个额度窗口（上游告知的恢复时刻超出了
// 瞬时限流的范围）；是则登记它，并返回那个时刻。
func (r *retryProvider) registerQuotaWindow(err error) (time.Time, bool) {
	d, ok := RetryAfter(err, time.Now())
	if !ok || d <= r.cooldown.window {
		return time.Time{}, false
	}
	until := time.Now().Add(d)
	r.cooldown.open(r.account, until, err, time.Now())
	return until, true
}

// hold 是额度窗口内的静默等待：至多一个窗口分界时长，本次调用的预算（ctx 的截止时间）
// 先用完就先交还。等待期间不发出请求，结束后把限流交还调用方——调用方对持续限流的
// 处置是现成的（交还动作、不计失败、稍后重来），这里只负责把循环的节奏放缓。
func (r *retryProvider) hold(ctx context.Context, until time.Time, cause error) error {
	d := time.Until(until)
	if d > r.cooldown.window {
		d = r.cooldown.window
	}
	if !r.cooldown.holdInWindow {
		d = 0
	}
	if d > 0 {
		// 预算在等待中用尽不是超时故障：调用方等的是上游恢复，如实交还限流，
		// 审计里才不会把额度耗尽记成网络故障。
		if err := SleepCtx(ctx, d); err != nil && !errors.Is(err, context.DeadlineExceeded) {
			return err
		}
	}
	return fmt.Errorf("上游处于额度窗口（至 %s 恢复）: %w", until.Local().Format("2006-01-02 15:04:05"), cause)
}

// heldError 标记一次没有发往上游的调用：额度窗口内被登记挡下。审计据此不留提示词——
// 没有发出去的请求没有失败现场，而每个在途槽位每分钟都会来一次。发现窗口的那次调用
// 不在此列：它真的被上游拒绝了，那正是最该留的证据。
type heldError struct{ err error }

func (e *heldError) Error() string { return e.err.Error() }

// Unwrap 让审计的「被挡下」判定与错误的分类、瞬时判定都穿透到内层。
func (e *heldError) Unwrap() error { return e.err }

// pause 是一轮重试之前的等待。上游明确要求等待的限流按它给的时长（只会在窗口分界
// 以内：更长的等待在撞上时就转入了额度窗口），不按退避表的估计值；其余按退避表。
// 两者都加抖动：上游给出同一个恢复时刻时，所有等待方会在同一瞬间醒来齐发，恢复的
// 那一刻成了一次自找的限流。
func (r *retryProvider) pause(err error, attempt int, schedule []time.Duration) time.Duration {
	if d, ok := RetryAfter(err, time.Now()); ok {
		return stagger(d)
	}
	if attempt < len(schedule) {
		return jitterDuration(schedule[attempt])
	}
	return 0
}

// stagger 在一个等待时长之上加随机错峰，落在 [d, 1.5d)：只推迟、不提前，上游要求的
// 最短等待始终被遵守。
func stagger(d time.Duration) time.Duration {
	// #nosec G404 -- 等待时序抖动非安全敏感：只把并发的等待摊开，弱随机足够且免错误分支。
	return d + time.Duration(rand.Int64N(int64(d/2)+1))
}

func (r *retryProvider) retryLoop(ctx context.Context, req Request, lastErr error, maxRetries int, schedule []time.Duration) (Response, error) {
	for attempt := 0; attempt < maxRetries; attempt++ {
		if err := SleepCtx(ctx, r.pause(lastErr, attempt, schedule)); err != nil {
			return Response{}, err
		}
		resp, err := r.inner.Complete(ctx, req)
		if err == nil {
			return resp, nil
		}
		// 重试后错误类别可能变化（如限流缓解后暴露 400 参数错误）：
		// 非重试类错误立即上抛，不按上一轮的退避表空转。
		if Classify(err) == ClassOther {
			return Response{}, err
		}
		if until, ok := r.registerQuotaWindow(err); ok {
			return Response{}, r.hold(ctx, until, err)
		}
		lastErr = err
	}
	return Response{}, lastErr
}

func (r *retryProvider) retry529(ctx context.Context, req Request, lastErr error) (Response, error) {
	for attempt := 0; attempt < r.opts.MaxRetries529; attempt++ {
		if err := SleepCtx(ctx, r.opts.Backoff529); err != nil {
			return Response{}, err
		}
		resp, err := r.inner.Complete(ctx, req)
		if err == nil {
			return resp, nil
		}
		if Classify(err) == ClassOther {
			return Response{}, err
		}
		if until, ok := r.registerQuotaWindow(err); ok {
			return Response{}, r.hold(ctx, until, err)
		}
		lastErr = err
	}
	return Response{}, lastErr
}

// Stream 对「起步失败」做与 Complete 同口径的重试：连接未建立、鉴权被拒、
// 限流这些发生在首个事件之前的失败，没有任何已产出内容需要回收，整轮重试
// 与 Complete 一样安全。流建立成功（返回了 channel）之后的中途失败不做重试
// ——已产出的事件无法收回，整体重跑的决策权在调用方。
func (r *retryProvider) Stream(ctx context.Context, req Request) (<-chan StreamEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	probe, err := r.awaitRecovery(ctx)
	if err != nil {
		return nil, err
	}
	ch, err := r.inner.Stream(ctx, req)
	if err == nil {
		if probe {
			r.cooldown.lift(r.account)
		}
		return ch, nil
	}
	switch Classify(err) {
	case ClassOther:
		return nil, err
	case Class429:
		if until, ok := r.registerQuotaWindow(err); ok {
			return nil, r.hold(ctx, until, err)
		}
		return r.retryStream(ctx, req, err, r.opts.MaxRetries429, r.opts.Backoff429)
	case Class529:
		return r.retryStream(ctx, req, err, r.opts.MaxRetries529, []time.Duration{r.opts.Backoff529})
	case Class5xx:
		return r.retryStream(ctx, req, err, r.opts.MaxRetries5xx, r.opts.Backoff5xx)
	case ClassNet:
		return r.retryStream(ctx, req, err, r.opts.MaxRetriesNet, r.opts.BackoffNet)
	}
	return nil, err
}

func (r *retryProvider) retryStream(ctx context.Context, req Request, lastErr error, maxRetries int, schedule []time.Duration) (<-chan StreamEvent, error) {
	for attempt := 0; attempt < maxRetries; attempt++ {
		if err := SleepCtx(ctx, r.pause(lastErr, attempt, schedule)); err != nil {
			return nil, err
		}
		ch, err := r.inner.Stream(ctx, req)
		if err == nil {
			return ch, nil
		}
		if Classify(err) == ClassOther {
			return nil, err
		}
		if until, ok := r.registerQuotaWindow(err); ok {
			return nil, r.hold(ctx, until, err)
		}
		lastErr = err
	}
	return nil, lastErr
}
