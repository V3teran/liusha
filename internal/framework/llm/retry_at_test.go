package llm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// 实测抓到的额度型限流：额度已用尽，错误体写明了重置时刻。速率型限流（1302）不带时刻。
const (
	quotaExhaustedBody = `{"type":"error","error":{"type":"rate_limit_error","code":"1308","message":"[1308][已达到 5 小时的使用上限。您的限额将在 2026-11-23 19:47:33 重置。][20261123194729faac]"}}`
	rateLimitedBody    = `{"type":"error","error":{"type":"rate_limit_error","code":"1302","message":"[1302][您的账户已达到速率限制，请您控制请求频率][202611231907459]"}}`
)

func TestRetryAtFromMessage(t *testing.T) {
	now := time.Date(2026, 11, 23, 19, 11, 41, 0, retryAtLocation)
	cases := []struct {
		name string
		msg  string
		now  time.Time
		want time.Time // 零值 = 未告知
	}{
		{"额度型限流带重置时刻", quotaExhaustedBody, now, time.Date(2026, 11, 23, 19, 47, 33, 0, retryAtLocation)},
		{"速率型限流不带时刻", rateLimitedBody, now, time.Time{}},
		{"重置时刻已经过去", quotaExhaustedBody, time.Date(2026, 11, 23, 19, 47, 34, 0, retryAtLocation), time.Time{}},
		{"恰好等于现在不算未来", quotaExhaustedBody, time.Date(2026, 11, 23, 19, 47, 33, 0, retryAtLocation), time.Time{}},
		{"无时刻的普通文本", "something went wrong", now, time.Time{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := retryAtFromMessage(tc.msg, tc.now); !got.Equal(tc.want) {
				t.Fatalf("retryAtFromMessage = %v, want %v", got, tc.want)
			}
		})
	}
}

// 不带时区的重置时刻按北京时间读：按 UTC 读会差 8 小时，睡过头比多撞几次贵得多。
func TestRetryAtFromMessageReadsBeijingTime(t *testing.T) {
	// 本机进程处在 UTC 下（数据库容器、CI 都是这样），解析结果不得随时区变化。
	t.Setenv("TZ", "UTC")
	now := time.Date(2026, 11, 23, 11, 0, 0, 0, time.UTC) // 北京时间 19:00
	got := retryAtFromMessage(quotaExhaustedBody, now)
	if want := time.Date(2026, 11, 23, 11, 47, 33, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("重置时刻应按北京时间读作 %v（UTC），实际 %v", want, got)
	}
}

func TestRetryAfterHeader(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		header http.Header
		want   time.Time // 零值 = 未告知
	}{
		{"延迟秒数", http.Header{"Retry-After": []string{"120"}}, now.Add(120 * time.Second)},
		{"非正延迟不构成等待依据", http.Header{"Retry-After": []string{"0"}}, time.Time{}},
		{"负延迟不构成等待依据", http.Header{"Retry-After": []string{"-5"}}, time.Time{}},
		{"HTTP 日期", http.Header{"Retry-After": []string{"Mon, 23 Nov 2026 12:05:00 GMT"}}, time.Date(2026, 11, 23, 12, 5, 0, 0, time.UTC)},
		{"头缺失", http.Header{}, time.Time{}},
		{"无法解析", http.Header{"Retry-After": []string{"soon"}}, time.Time{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := retryAfterHeader(tc.header, now); !got.Equal(tc.want) {
				t.Fatalf("retryAfterHeader = %v, want %v", got, tc.want)
			}
		})
	}
}

// 标准的 Retry-After 头优先于错误体里的重置时间；没有头时回退到错误体。
func TestRetryAtOfPrefersHeader(t *testing.T) {
	now := time.Date(2026, 11, 23, 19, 11, 41, 0, retryAtLocation)
	header := http.Header{"Retry-After": []string{"30"}}
	want := now.Add(30 * time.Second)
	if got := retryAtOf(header, quotaExhaustedBody, now); !got.Equal(want) {
		t.Fatalf("应优先采信 Retry-After 头 %v，实际 %v", want, got)
	}
	if got := retryAtOf(http.Header{}, quotaExhaustedBody, now); got.IsZero() {
		t.Fatal("无 Retry-After 头时应回退到错误体里的重置时间")
	}
}

func TestRetryAfter(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	resetAt := now.Add(35 * time.Minute)
	cases := []struct {
		name        string
		err         error
		want        time.Duration
		informative bool
	}{
		{"限流带恢复时刻", &HTTPError{Code: 429, RetryAt: resetAt}, 35 * time.Minute, true},
		{"被包装的限流", fmt.Errorf("anthropic generate: %w", &HTTPError{Code: 429, RetryAt: resetAt}), 35 * time.Minute, true},
		{"限流但上游未告知", &HTTPError{Code: 429}, 0, false},
		{"恢复时刻已过", &HTTPError{Code: 429, RetryAt: now.Add(-time.Second)}, 0, false},
		{"非限流错误即使带时刻也不构成等待依据", &HTTPError{Code: 503, RetryAt: resetAt}, 0, false},
		{"普通错误", errors.New("boom"), 0, false},
		{"nil", nil, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, ok := RetryAfter(tc.err, now)
			if ok != tc.informative || d != tc.want {
				t.Fatalf("RetryAfter = (%v, %v), want (%v, %v)", d, ok, tc.want, tc.informative)
			}
		})
	}
}

// ── 额度归属 ───────────────────────────────────────────────────────────────

// 额度随账户（服务地址 + 密钥）走，不随 provider 行走；标识里不得出现密钥明文。
func TestQuotaIdentity(t *testing.T) {
	const gw = "https://gw.example/v1"
	alpha := quotaIdentity(gw, "account-alpha")
	if alpha != quotaIdentity(gw, "account-alpha") {
		t.Fatal("同一地址同一密钥应是同一份额度")
	}
	if alpha == quotaIdentity(gw, "account-beta") {
		t.Fatal("换了密钥就是另一份额度")
	}
	if alpha == quotaIdentity("https://other.example/v1", "account-alpha") {
		t.Fatal("换了服务地址就是另一份额度")
	}
	if strings.Contains(alpha, "account-alpha") {
		t.Fatal("额度标识里不得出现密钥明文")
	}
}

// ── 额度窗口登记 ───────────────────────────────────────────────────────────

func TestCooldownAdmit(t *testing.T) {
	const probeEvery = 5 * time.Minute
	t0 := time.Date(2026, 11, 23, 19, 11, 41, 0, time.UTC)
	until := t0.Add(36 * time.Minute) // 缺省分界 defaultQuotaWindow = 1 分钟
	cause := &HTTPError{Code: 429, RetryAt: until}

	decision := func(c *cooldown, now time.Time) admission {
		d, _ := c.admit("acct", now)
		return d
	}

	t.Run("没有窗口照常发", func(t *testing.T) {
		if got := decision(newCooldown(), t0); got != admitFree {
			t.Fatalf("没有登记时应照常发，实际 %v", got)
		}
	})

	t.Run("窗口内不发请求", func(t *testing.T) {
		c := newCooldown()
		c.open("acct", until, cause, t0)
		for i := 0; i < 5; i++ { // 同一瞬间的多个调用方：全部等待
			if got := decision(c, t0.Add(time.Second)); got != admitHold {
				t.Fatalf("窗口内应等待，实际 %v", got)
			}
		}
	})

	t.Run("间隔未到不放行探测", func(t *testing.T) {
		c := newCooldown()
		c.open("acct", until, cause, t0)
		if got := decision(c, t0.Add(probeEvery-time.Nanosecond)); got != admitHold {
			t.Fatalf("探测间隔未到应继续等待，实际 %v", got)
		}
	})

	t.Run("间隔一到放行且只放行一次", func(t *testing.T) {
		c := newCooldown()
		c.open("acct", until, cause, t0)
		at := t0.Add(probeEvery)
		d, e := c.admit("acct", at)
		if d != admitProbe {
			t.Fatalf("探测间隔一到应放行探测，实际 %v", d)
		}
		if !errors.Is(e.cause, cause) || !e.until.Equal(until) {
			t.Fatalf("准入结果应带回窗口的恢复时刻与原因，实际 %+v", e)
		}
		if got := decision(c, at); got != admitHold {
			t.Fatalf("同一瞬间只放行一次，其余应等待，实际 %v", got)
		}
	})

	t.Run("探测仍被限流则重新起算间隔", func(t *testing.T) {
		c := newCooldown()
		c.open("acct", until, cause, t0)
		at := t0.Add(probeEvery)
		if decision(c, at) != admitProbe {
			t.Fatal("首次探测应被放行")
		}
		c.open("acct", until.Add(time.Hour), cause, at) // 探测撞上同一个窗口
		if decision(c, at.Add(time.Second)) != admitHold {
			t.Fatal("探测后被重新告知窗口，应从这次告知起算下一个探测间隔")
		}
		if decision(c, at.Add(probeEvery)) != admitProbe {
			t.Fatal("下一个间隔到了应再次放行")
		}
	})

	t.Run("窗口尾声等到恢复时刻再发", func(t *testing.T) {
		// 让「尾声之前」落在一个探测间隔之内：这里要验证的是尾声规则，不是探测规则。
		base := until.Add(-defaultQuotaWindow - 2*time.Second)
		c := newCooldown()
		c.open("acct", until, cause, base)
		if got := decision(c, until.Add(-defaultQuotaWindow-time.Nanosecond)); got != admitHold {
			t.Fatalf("最后一段之前仍是窗口内等待，实际 %v", got)
		}
		if got := decision(c, until.Add(-defaultQuotaWindow)); got != admitTail {
			t.Fatalf("只剩最后一段时应等到恢复时刻再发，实际 %v", got)
		}
	})

	t.Run("恢复时刻一到照常发并清掉登记", func(t *testing.T) {
		c := newCooldown()
		c.open("acct", until, cause, t0)
		if got := decision(c, until); got != admitFree {
			t.Fatalf("恢复时刻已到应照常发，实际 %v", got)
		}
		c.mu.Lock()
		_, remains := c.entries["acct"]
		c.mu.Unlock()
		if remains {
			t.Fatal("到期的窗口应被清掉")
		}
	})

	t.Run("探测成功即解除", func(t *testing.T) {
		c := newCooldown()
		c.open("acct", until, cause, t0)
		c.lift("acct")
		if got := decision(c, t0.Add(time.Second)); got != admitFree {
			t.Fatalf("解除后应照常发，实际 %v", got)
		}
	})

	t.Run("账户之间互不影响", func(t *testing.T) {
		c := newCooldown()
		c.open("acct-a", until, cause, t0)
		if got := decision(c, t0); got != admitFree { // decision 查的是 "acct"
			t.Fatalf("别的账户的窗口不应约束这个账户，实际 %v", got)
		}
		if d, _ := c.admit("acct-a", t0); d != admitHold {
			t.Fatalf("被登记的账户应等待，实际 %v", d)
		}
	})
}

// ── 重试装饰器与额度窗口 ───────────────────────────────────────────────────

// scriptedProvider 按脚本依次返回每次调用的结果，并记录实际发出的请求数。
type scriptedProvider struct {
	mu       sync.Mutex
	calls    int
	outcomes []error     // nil = 成功；用尽后重复最后一项
	hook     func(n int) // 第 n 次请求在返回结果之前回调（不持锁）
}

func (p *scriptedProvider) Complete(context.Context, Request) (Response, error) {
	p.mu.Lock()
	p.calls++
	out := p.outcomes[len(p.outcomes)-1]
	if p.calls-1 < len(p.outcomes) {
		out = p.outcomes[p.calls-1]
	}
	hook := p.hook
	p.mu.Unlock()
	if hook != nil {
		hook(p.calls)
	}
	return Response{}, out
}

func (p *scriptedProvider) sent() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func (p *scriptedProvider) Stream(ctx context.Context, req Request) (<-chan StreamEvent, error) {
	if _, err := p.Complete(ctx, req); err != nil {
		return nil, err
	}
	ch := make(chan StreamEvent)
	close(ch)
	return ch, nil
}
func (p *scriptedProvider) ModelID() string    { return "m" }
func (p *scriptedProvider) ProviderID() string { return "p" }

const testAccount = "acct"

// testGovernor 是只带额度窗口登记、不限在途名额的节律治理——额度窗口的用例只验证额度窗口。
func testGovernor(cd *cooldown) *governor {
	return &governor{cooldown: cd, inflight: newInflight(0)}
}

// retryFor 按测试用的退避表包一个 Provider，归属测试账户。
func retryFor(inner Provider, cd *cooldown) Provider {
	return WithRetry(inner, retryOptsForTest(), testGovernor(cd), testAccount)
}

// fastCooldown 把两个时间阈值缩小到毫秒级，让测试在真实时间里走完同样的状态迁移。
func fastCooldown() *cooldown {
	c := newCooldown()
	c.window, c.probeEvery = 60*time.Millisecond, 200*time.Millisecond
	return c
}

func retryOptsForTest() RetryOptions {
	return RetryOptions{
		MaxRetries429: 3, MaxRetries5xx: 2, MaxRetriesNet: 2, MaxRetries529: 1,
		Backoff429: []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond},
		Backoff5xx: []time.Duration{time.Millisecond, time.Millisecond},
		BackoffNet: []time.Duration{time.Millisecond, time.Millisecond},
	}
}

func quotaError(after time.Duration) error {
	return &HTTPError{Code: 429, RetryAt: time.Now().Add(after)}
}

func withBudget(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

// assertQuotaWindow 断言 err 是一次因额度窗口交还的调用：属于限流（瞬时故障，审计记
// rate_limited），并说明上游处于额度窗口。
// 并说明上游处于额度窗口。
func assertQuotaWindow(t *testing.T, err error) {
	t.Helper()
	if !IsTransient(err) || Classify(err) != Class429 {
		t.Fatalf("因额度窗口交还的调用应归为限流（瞬时故障），实际 %v（分类 %v）", err, Classify(err))
	}
	if !strings.Contains(err.Error(), "额度窗口") {
		t.Fatalf("错误应说明上游处于额度窗口，实际 %q", err.Error())
	}
}

// assertNeverSent 断言这次调用被登记挡下、根本没有发往上游。
func assertNeverSent(t *testing.T, err error) {
	t.Helper()
	assertQuotaWindow(t, err)
	if !isHeld(err) {
		t.Fatalf("没有发往上游的调用应带「被挡下」标记，实际 %v", err)
	}
}

// 额度型限流不按退避表盲目重试：登记窗口、静默等待一个周期后把限流交还，只发过那一次请求。
func TestQuotaWindowHoldsInsteadOfBlindRetrying(t *testing.T) {
	inner := &scriptedProvider{outcomes: []error{quotaError(10 * time.Second)}}
	p := retryFor(inner, fastCooldown())
	ctx, cancel := withBudget(time.Second)
	defer cancel()

	start := time.Now()
	_, err := p.Complete(ctx, Request{})
	assertQuotaWindow(t, err)
	if isHeld(err) {
		t.Fatal("真的发往上游并被拒绝的调用不带「被挡下」标记——它是该保留的失败现场")
	}
	if waited := time.Since(start); waited < 40*time.Millisecond {
		t.Fatalf("应静默等待一个周期（约 60ms）再交还，实际只等了 %v", waited)
	}
	if inner.sent() != 1 {
		t.Fatalf("额度窗口内不应再发请求，实际发出 %d 次", inner.sent())
	}
}

// 额度是账户级的状态：一个调用方撞上之后，同一账户的其余调用方（哪怕是通过另一个
// Provider 实例——Router 每 30 秒重建一次）照着登记等待，不再各发一次去发现它。
func TestQuotaWindowSharedAcrossCallers(t *testing.T) {
	cd := fastCooldown()
	first := &scriptedProvider{outcomes: []error{quotaError(10 * time.Second)}}
	ctx, cancel := withBudget(time.Second)
	defer cancel()
	if _, err := retryFor(first, cd).Complete(ctx, Request{}); err == nil {
		t.Fatal("首次调用应撞上额度窗口")
	}

	// 另一个实例、同一个账户：不发请求，照着登记等待。
	second := &scriptedProvider{outcomes: []error{nil}}
	start := time.Now()
	_, err := retryFor(second, cd).Complete(ctx, Request{})
	assertNeverSent(t, err)
	if time.Since(start) < 40*time.Millisecond {
		t.Fatal("后来者应照着登记静默等待，而不是即刻返回")
	}
	if second.sent() != 0 {
		t.Fatalf("后来者不应再发请求去发现同一个窗口，实际发出 %d 次", second.sent())
	}

	// 另一个账户：不受约束。
	other := &scriptedProvider{outcomes: []error{nil}}
	if _, err := WithRetry(other, retryOptsForTest(), testGovernor(cd), "another-account").Complete(ctx, Request{}); err != nil {
		t.Fatalf("别的账户的额度窗口不应约束这个账户：%v", err)
	}
	if other.sent() != 1 {
		t.Fatalf("别的账户应照常发请求，实际发出 %d 次", other.sent())
	}
}

// 上游告知的恢复时刻只是它的说法（额度可能提前到账）：间隔一到放行一次探测，
// 探测成功就解除窗口，之后的调用照常发出。
func TestQuotaWindowLiftedByProbe(t *testing.T) {
	cd := fastCooldown() // probeEvery = 200ms
	inner := &scriptedProvider{outcomes: []error{quotaError(10 * time.Second), nil, nil}}
	p := retryFor(inner, cd)
	ctx, cancel := withBudget(2 * time.Second)
	defer cancel()

	if _, err := p.Complete(ctx, Request{}); err == nil {
		t.Fatal("首次调用应撞上额度窗口")
	}
	time.Sleep(220 * time.Millisecond)

	if _, err := p.Complete(ctx, Request{}); err != nil { // 放行的探测
		t.Fatalf("探测应成功并解除窗口，实际 %v", err)
	}
	if _, err := p.Complete(ctx, Request{}); err != nil { // 窗口已解除：照常
		t.Fatalf("窗口解除后应照常调用，实际 %v", err)
	}
	if inner.sent() != 3 {
		t.Fatalf("应发出 3 次请求（首次、探测、解除后的常规调用），实际 %d 次", inner.sent())
	}
}

// 探测每个间隔只放行一次：同一瞬间到来的其余调用方继续等待，不会齐声去探。
func TestQuotaWindowProbeAdmitsOnePerInterval(t *testing.T) {
	cd := fastCooldown() // probeEvery = 200ms
	inner := &scriptedProvider{outcomes: []error{quotaError(10 * time.Second)}}
	p := retryFor(inner, cd)
	ctx, cancel := withBudget(time.Second)
	defer cancel()

	if _, err := p.Complete(ctx, Request{}); err == nil {
		t.Fatal("首次调用应撞上额度窗口")
	}
	time.Sleep(220 * time.Millisecond)

	// 探测被限流，上游又告知了一个窗口；紧随其后的调用方必须等待。
	_, err := p.Complete(ctx, Request{})
	assertQuotaWindow(t, err)
	if isHeld(err) {
		t.Fatal("真的发往上游并被拒绝的调用不带「被挡下」标记——它是该保留的失败现场")
	}
	_, err = p.Complete(ctx, Request{})
	assertNeverSent(t, err)
	if inner.sent() != 2 {
		t.Fatalf("每个间隔只应放行一次探测，应发出 2 次请求（首次、探测），实际 %d 次", inner.sent())
	}
}

// 窗口只剩最后一段时不必交还外层绕一圈：等到恢复时刻直接发。
func TestQuotaWindowTailWaitsThenSends(t *testing.T) {
	// 分界 1s、恢复时刻在 1.5s 之后：首次调用在窗口里等满一个分界时长，剩下的约 0.5s 落在
	// 尾声之内。恢复时刻比分界多出的 0.5s 是留给调度的余量——夹具构造到被测代码读到它之间
	// 隔着调度，慢的运行环境上余量太小就会读掉到分界以下（见 TestQuotaWindowWithoutHoldReturnsImmediately）。
	cd := newCooldown()
	cd.window, cd.probeEvery = time.Second, time.Hour
	inner := &scriptedProvider{outcomes: []error{quotaError(1500 * time.Millisecond), nil}}
	p := retryFor(inner, cd)
	ctx, cancel := withBudget(5 * time.Second)
	defer cancel()

	if _, err := p.Complete(ctx, Request{}); err == nil {
		t.Fatal("首次调用应撞上额度窗口（1.5s 超出 1s 的分界）")
	}
	if _, err := p.Complete(ctx, Request{}); err != nil {
		t.Fatalf("窗口尾声应等到恢复时刻后发出并成功，实际 %v", err)
	}
	if inner.sent() != 2 {
		t.Fatalf("应发出 2 次请求，实际 %d 次", inner.sent())
	}
}

// 尾声的等待被调用预算打断：没有发出的调用同样如实说明是在等上游恢复，不记成超时。
func TestQuotaTailInterruptedByBudgetReportsRateLimit(t *testing.T) {
	// 分界 1s、恢复时刻在 1.5s 之后：首次调用等满一个分界时长后，窗口只剩约 0.5s，落在尾声之内。
	cd := newCooldown()
	cd.window, cd.probeEvery = time.Second, time.Hour
	inner := &scriptedProvider{outcomes: []error{quotaError(1500 * time.Millisecond)}}
	p := retryFor(inner, cd)
	ctx, cancel := withBudget(4 * time.Second)
	if _, err := p.Complete(ctx, Request{}); err == nil {
		t.Fatal("首次调用应撞上额度窗口")
	}
	cancel() // 此刻窗口只剩约 0.5s，落在尾声之内

	narrow, cancelNarrow := withBudget(5 * time.Millisecond)
	defer cancelNarrow()
	_, err := p.Complete(narrow, Request{})
	assertNeverSent(t, err)
	if inner.sent() != 1 {
		t.Fatalf("被打断的尾声等待不应发出请求，实际发出 %d 次", inner.sent())
	}
}

// 静默等待不超过本次调用的剩余预算；预算在等待中用尽时如实交还限流，不记成超时。
func TestQuotaHoldBoundedByCallBudget(t *testing.T) {
	cd := newCooldown()
	cd.window, cd.probeEvery = 300*time.Millisecond, time.Hour
	inner := &scriptedProvider{outcomes: []error{quotaError(10 * time.Second)}}
	p := retryFor(inner, cd)
	ctx, cancel := withBudget(40 * time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := p.Complete(ctx, Request{})
	assertQuotaWindow(t, err)
	if isHeld(err) {
		t.Fatal("真的发往上游并被拒绝的调用不带「被挡下」标记——它是该保留的失败现场")
	}
	if waited := time.Since(start); waited >= 200*time.Millisecond {
		t.Fatalf("等待应受本次调用的剩余预算（40ms）约束，实际等了 %v", waited)
	}
}

// 调用方在等待中取消：立即返回取消，不再发出请求。
func TestQuotaHoldHonorsCancel(t *testing.T) {
	cd := newCooldown()
	cd.window, cd.probeEvery = time.Hour, time.Hour
	inner := &scriptedProvider{outcomes: []error{quotaError(10 * time.Second)}}
	p := retryFor(inner, cd)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { _, err := p.Complete(ctx, Request{}); done <- err }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("取消后应以取消错误返回，实际 %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("取消未能打断额度窗口内的等待")
	}
	if inner.sent() != 1 {
		t.Fatalf("取消后不应再发请求，实际发出 %d 次", inner.sent())
	}
}

// 上游明确要求的短时等待（一分钟以内）按它给的时长等，等完在本次调用内接着发；
// 这是瞬时限流，不登记为额度窗口。
func TestShortAnnouncedWaitIsHonoredInCall(t *testing.T) {
	// 分界 1s、恢复时刻在 300ms 之后：留给调度的余量上百毫秒（夹具构造到被测代码读到恢复
	// 时刻之间，见 TestQuotaWindowWithoutHoldReturnsImmediately）。
	cd := newCooldown()
	cd.window, cd.probeEvery = time.Second, time.Hour
	inner := &scriptedProvider{outcomes: []error{quotaError(300 * time.Millisecond), nil}}
	p := retryFor(inner, cd)
	ctx, cancel := withBudget(2 * time.Second)
	defer cancel()

	start := time.Now()
	if _, err := p.Complete(ctx, Request{}); err != nil {
		t.Fatalf("瞬时限流等完应重试成功，实际 %v", err)
	}
	if waited := time.Since(start); waited < 200*time.Millisecond {
		t.Fatalf("应按上游要求的时长（300ms）等待，实际只等了 %v", waited)
	}
	if inner.sent() != 2 {
		t.Fatalf("应发出 2 次请求，实际 %d 次", inner.sent())
	}
	if d, _ := cd.admit(testAccount, time.Now()); d != admitFree {
		t.Fatalf("瞬时限流不应登记为额度窗口，实际准入结果 %v", d)
	}
}

// 上游反复要求短时等待时，次数上限依然管用：不会无限循环。
func TestRepeatedShortAnnouncedWaitsAreBounded(t *testing.T) {
	inner := &scriptedProvider{outcomes: []error{quotaError(5 * time.Millisecond)}}
	p := retryFor(inner, fastCooldown()) // MaxRetries429 = 3
	ctx, cancel := withBudget(5 * time.Second)
	defer cancel()

	if _, err := p.Complete(ctx, Request{}); err == nil {
		t.Fatal("限流未缓解，应以限流错误结束")
	}
	if inner.sent() != 4 {
		t.Fatalf("应发出 4 次请求（首发 + 3 次重试），实际 %d 次", inner.sent())
	}
}

// 速率型限流（上游未告知恢复时刻）行为不变：按退避表重试，耗尽即失败。
func TestUnannouncedRateLimitUsesSchedule(t *testing.T) {
	inner := &scriptedProvider{outcomes: []error{&HTTPError{Code: 429}}}
	p := retryFor(inner, newCooldown())
	ctx, cancel := withBudget(5 * time.Second)
	defer cancel()

	if _, err := p.Complete(ctx, Request{}); err == nil {
		t.Fatal("限流未缓解，应以限流错误结束")
	}
	if inner.sent() != 4 {
		t.Fatalf("应发出 4 次请求（首发 + 3 次重试），实际 %d 次", inner.sent())
	}
}

// 重试途中撞上额度窗口（首发是别的瞬时错误）：同样转入等待，不继续盲试。
func TestQuotaWindowHitMidRetry(t *testing.T) {
	inner := &scriptedProvider{outcomes: []error{&HTTPError{Code: 503}, quotaError(10 * time.Second), nil}}
	p := retryFor(inner, fastCooldown())
	ctx, cancel := withBudget(time.Second)
	defer cancel()

	_, err := p.Complete(ctx, Request{})
	assertQuotaWindow(t, err)
	if isHeld(err) {
		t.Fatal("真的发往上游并被拒绝的调用不带「被挡下」标记——它是该保留的失败现场")
	}
	if inner.sent() != 2 {
		t.Fatalf("撞上额度窗口后不应再发请求，应发出 2 次，实际 %d 次", inner.sent())
	}
}

// 流式路径与 Complete 同口径：额度窗口内同样等待、同样不发请求。
func TestQuotaWindowOnStreamPath(t *testing.T) {
	inner := &scriptedProvider{outcomes: []error{quotaError(10 * time.Second)}}
	p := retryFor(inner, fastCooldown())
	ctx, cancel := withBudget(time.Second)
	defer cancel()

	_, err := p.Stream(ctx, Request{})
	assertQuotaWindow(t, err)
	if isHeld(err) {
		t.Fatal("真的发往上游并被拒绝的调用不带「被挡下」标记——它是该保留的失败现场")
	}
	_, err = p.Stream(ctx, Request{})
	assertNeverSent(t, err)
	if inner.sent() != 1 {
		t.Fatalf("流式路径同样只应发出 1 次请求，实际 %d 次", inner.sent())
	}
}

// ── 审计：被挡下的调用不留提示词 ───────────────────────────────────────────

type recordingSink struct {
	mu   sync.Mutex
	recs []UsageRecord
}

func (s *recordingSink) RecordUsage(_ context.Context, r UsageRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recs = append(s.recs, r)
}

func TestInstrumentProviderHeldCallKeepsNoMessages(t *testing.T) {
	prompt := []Message{{Role: RoleUser, Content: "一段很长的提示词"}}
	req := Request{Messages: prompt}

	t.Run("发往上游并失败的调用保留失败现场", func(t *testing.T) {
		sink := &recordingSink{}
		inner := &scriptedProvider{outcomes: []error{&HTTPError{Code: 400}}}
		p := InstrumentProvider(retryFor(inner, newCooldown()), sink)
		_, _ = p.Complete(context.Background(), req)
		if len(sink.recs) != 1 || len(sink.recs[0].Messages) == 0 {
			t.Fatalf("发往上游的失败调用应留存提示词，实际 %+v", sink.recs)
		}
	})

	t.Run("额度窗口内被挡下的调用记一行但不留提示词", func(t *testing.T) {
		sink := &recordingSink{}
		cd := newCooldown()
		cd.window, cd.probeEvery = time.Millisecond, time.Hour
		inner := &scriptedProvider{outcomes: []error{quotaError(10 * time.Second)}}
		p := InstrumentProvider(retryFor(inner, cd), sink)
		// 首次调用真的发出了（被拒）；随后同一账户的第二次调用是被挡下的。
		_, _ = p.Complete(context.Background(), req)
		_, _ = p.Complete(context.Background(), req)
		if len(sink.recs) != 2 {
			t.Fatalf("两次调用应各记一行，实际 %d 行", len(sink.recs))
		}
		if len(sink.recs[0].Messages) == 0 {
			t.Fatal("真的被上游拒绝的那次应留存提示词")
		}
		held := sink.recs[1]
		if len(held.Messages) != 0 {
			t.Fatalf("没有发往上游的调用不应留存提示词，实际留存 %d 字节", len(held.Messages))
		}
		if !strings.Contains(held.Err, "额度窗口") {
			t.Fatalf("审计行应说明上游处于额度窗口，实际 %q", held.Err)
		}
	})
}

// 随机错峰只推迟、不提前：上游要求的最短等待始终被遵守；且确实在摊开，不是摆设。
func TestStaggerNeverShortensWait(t *testing.T) {
	const d = 10 * time.Second
	seen := make(map[time.Duration]struct{})
	for i := 0; i < 200; i++ {
		got := stagger(d)
		if got < d || got >= d+d/2 {
			t.Fatalf("错峰后的等待应落在 [%v, %v)，实际 %v", d, d+d/2, got)
		}
		seen[got] = struct{}{}
	}
	if len(seen) < 100 {
		t.Fatalf("多次错峰的结果应当各不相同，实际 200 次里只有 %d 种", len(seen))
	}
}

// 关闭调用内等待（对话侧）：额度窗口内的调用立即交还限流——既不盲目重试，也不在窗口中段
// 或尾声里等。
//
// 这里的时间余量都在几百毫秒以上。夹具在构造时写死恢复时刻，读到它的是被测代码：两者之间
// 隔着调度，慢的运行环境上几十毫秒就过去了。恢复时刻曾只比分界多 30ms，读到时已掉到分界
// 以下，这次限流被当成瞬时的，断言落空。尾声那一步不经首次调用去等，直接把窗口登记成
// 「离恢复不到一个分界时长」。
func TestQuotaWindowWithoutHoldReturnsImmediately(t *testing.T) {
	const window = 5 * time.Second
	cd := newCooldown()
	cd.window, cd.probeEvery = window, time.Hour
	cd.holdInWindow = false
	inner := &scriptedProvider{outcomes: []error{quotaError(time.Hour)}}
	p := retryFor(inner, cd)
	ctx, cancel := withBudget(time.Second)
	defer cancel()

	// 发现窗口的调用：真的发出过，立即交还。
	_, err := p.Complete(ctx, Request{})
	assertQuotaWindow(t, err)
	if isHeld(err) {
		t.Fatal("发现窗口的调用真的发往了上游，不应带「被挡下」标记")
	}
	// 开着等待时，窗口中段的调用会静默等到预算用尽（1s），尾声的调用会等到恢复时刻：
	// 两者在这里都应当立即交还。500ms 远小于任何一种等待，又容得下慢环境上的调度。
	const immediate = 500 * time.Millisecond

	// 窗口中段（离恢复还有一个小时）：立即交还，没有发出请求。
	start := time.Now()
	_, err = p.Complete(ctx, Request{})
	assertNeverSent(t, err)
	if waited := time.Since(start); waited >= immediate {
		t.Fatalf("关闭等待后窗口中段的调用不应等候，实际等了 %v", waited)
	}

	// 窗口尾声（离恢复 4s，不到一个分界时长）：同样立即交还。
	cd.open(testAccount, time.Now().Add(4*time.Second), err, time.Now())
	start = time.Now()
	_, err = p.Complete(ctx, Request{})
	assertNeverSent(t, err)
	if waited := time.Since(start); waited >= immediate {
		t.Fatalf("关闭等待后尾声的调用不应等候，实际等了 %v", waited)
	}
	if inner.sent() != 1 {
		t.Fatalf("只应发出发现窗口的那 1 次请求，实际 %d 次", inner.sent())
	}
}
