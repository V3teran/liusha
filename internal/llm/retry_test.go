package llm

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

// noSleep 让重试测试瞬时跑完，绕开真实 backoff schedule。
func noSleep() RetryOptions {
	o := DefaultRetryOptions()
	o.Backoff429 = []time.Duration{0, 0, 0}
	o.Backoff5xx = []time.Duration{0, 0}
	o.BackoffNet = []time.Duration{0, 0}
	o.Backoff529 = 0
	return o
}

// TestRetry_429ThreeTimesThenFallback：primary 连出 4 次 429 → fallback 1 次成功
// 期望：result.Content 来自 fallback，primary 调 4 次，fallback 调 1 次
func TestRetry_429ThreeTimesThenFallback(t *testing.T) {
	primary := &testGen{tag: "primary", seq: []error{
		&HTTPError{Code: 429}, &HTTPError{Code: 429}, &HTTPError{Code: 429}, &HTTPError{Code: 429},
	}}
	fallback := &testGen{tag: "fb"}

	g := WithRetry(primary, fallback, noSleep())
	res, err := g.Generate(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("应成功（fallback 兜底），实际 err=%v", err)
	}
	if res.Content != "ok-fb" {
		t.Errorf("应来自 fallback，实际 content=%q", res.Content)
	}
	if primary.calls != 4 {
		t.Errorf("primary 应被调 4 次（首次 + 3 次重试），实际 %d", primary.calls)
	}
	if fallback.calls != 1 {
		t.Errorf("fallback 应被调 1 次，实际 %d", fallback.calls)
	}
}

// TestRetry_429SecondAttemptSucceeds：primary [429, OK] → 重试 1 次后成功
func TestRetry_429SecondAttemptSucceeds(t *testing.T) {
	primary := &testGen{tag: "primary", seq: []error{&HTTPError{Code: 429}, nil}}
	fallback := &testGen{tag: "fb"}

	g := WithRetry(primary, fallback, noSleep())
	res, err := g.Generate(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("应成功，实际 err=%v", err)
	}
	if res.Content != "ok-primary" {
		t.Errorf("应来自 primary，实际 content=%q", res.Content)
	}
	if primary.calls != 2 {
		t.Errorf("primary 应被调 2 次，实际 %d", primary.calls)
	}
	if fallback.calls != 0 {
		t.Errorf("fallback 不应被触达，实际 %d", fallback.calls)
	}
}

// TestRetry_529OnceThenFallback：primary 2 次 529 → fallback 成功
func TestRetry_529OnceThenFallback(t *testing.T) {
	primary := &testGen{tag: "primary", seq: []error{
		&HTTPError{Code: 529}, &HTTPError{Code: 529},
	}}
	fallback := &testGen{tag: "fb"}

	g := WithRetry(primary, fallback, noSleep())
	res, err := g.Generate(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("应由 fallback 兜底成功，实际 err=%v", err)
	}
	if res.Content != "ok-fb" {
		t.Errorf("应来自 fallback，实际 content=%q", res.Content)
	}
	if primary.calls != 2 {
		t.Errorf("primary 应被调 2 次（首次 + 1 次重试），实际 %d", primary.calls)
	}
	if fallback.calls != 1 {
		t.Errorf("fallback 应被调 1 次，实际 %d", fallback.calls)
	}
}

// TestRetry_5xxRetriesNoFallback：500/502/503/504 重试 2 次，不切 fallback
func TestRetry_5xxRetriesNoFallback(t *testing.T) {
	primary := &testGen{tag: "primary", seq: []error{
		&HTTPError{Code: 500}, &HTTPError{Code: 502}, &HTTPError{Code: 503},
	}}
	fallback := &testGen{tag: "fb"}

	g := WithRetry(primary, fallback, noSleep())
	_, err := g.Generate(context.Background(), nil, nil)
	if err == nil {
		t.Fatal("5xx 耗尽重试应抛错")
	}
	if primary.calls != 3 {
		t.Errorf("primary 应被调 3 次（首次 + 2 次重试），实际 %d", primary.calls)
	}
	if fallback.calls != 0 {
		t.Errorf("5xx 不应切 fallback，实际调用 %d 次", fallback.calls)
	}
}

// TestRetry_5xxSecondAttemptSucceeds：500 一次后恢复
func TestRetry_5xxSecondAttemptSucceeds(t *testing.T) {
	primary := &testGen{tag: "primary", seq: []error{&HTTPError{Code: 500}, nil}}
	g := WithRetry(primary, nil, noSleep())
	res, err := g.Generate(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("应成功，实际 err=%v", err)
	}
	if res.Content != "ok-primary" {
		t.Errorf("应来自 primary，实际 %q", res.Content)
	}
}

// TestRetry_4xxNoRetry：401/403/404 直接抛错，不重试
func TestRetry_4xxNoRetry(t *testing.T) {
	for _, code := range []int{400, 401, 403, 404} {
		t.Run("code", func(t *testing.T) {
			primary := &testGen{tag: "primary", seq: []error{&HTTPError{Code: code}}}
			fallback := &testGen{tag: "fb"}
			g := WithRetry(primary, fallback, noSleep())
			_, err := g.Generate(context.Background(), nil, nil)
			if err == nil {
				t.Fatalf("code=%d 应直接抛错", code)
			}
			if primary.calls != 1 {
				t.Errorf("code=%d primary 应只调 1 次（不重试），实际 %d", code, primary.calls)
			}
			if fallback.calls != 0 {
				t.Errorf("code=%d 不应切 fallback，实际 %d", code, fallback.calls)
			}
		})
	}
}

// TestRetry_NetworkTimeout：网络超时连两次后第三次成功
func TestRetry_NetworkTimeout(t *testing.T) {
	primary := &testGen{tag: "primary", seq: []error{
		netTimeoutErr(), netTimeoutErr(), nil,
	}}
	g := WithRetry(primary, nil, noSleep())
	res, err := g.Generate(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("应成功，实际 err=%v", err)
	}
	if res.Content != "ok-primary" {
		t.Errorf("应来自 primary，实际 %q", res.Content)
	}
	if primary.calls != 3 {
		t.Errorf("primary 应被调 3 次，实际 %d", primary.calls)
	}
}

// TestRetry_NoFallbackOnExhaust：未配置 fallback 时 429 耗尽抛错
func TestRetry_NoFallbackOnExhaust(t *testing.T) {
	primary := &testGen{tag: "primary", seq: []error{
		&HTTPError{Code: 429}, &HTTPError{Code: 429}, &HTTPError{Code: 429}, &HTTPError{Code: 429},
	}}
	g := WithRetry(primary, nil, noSleep())
	_, err := g.Generate(context.Background(), nil, nil)
	if err == nil {
		t.Fatal("无 fallback 时应抛错")
	}
	if primary.calls != 4 {
		t.Errorf("primary 应被调 4 次，实际 %d", primary.calls)
	}
}

// TestRetry_CtxCanceledStops：ctx 取消后立刻停（不再 sleep / 不再调）
func TestRetry_CtxCanceledStops(t *testing.T) {
	primary := &testGen{tag: "primary", seq: []error{
		&HTTPError{Code: 429}, &HTTPError{Code: 429}, &HTTPError{Code: 429}, &HTTPError{Code: 429},
	}}
	opts := DefaultRetryOptions()
	opts.Backoff429 = []time.Duration{50 * time.Millisecond, 50 * time.Millisecond, 50 * time.Millisecond}
	g := WithRetry(primary, nil, opts)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	_, err := g.Generate(ctx, nil, nil)
	if err == nil {
		t.Fatal("ctx 取消应抛错")
	}
	// 取消后 primary 应只被调最多 1 次（首次执行后 sleep 时被取消）。
	if primary.calls > 1 {
		t.Errorf("ctx 取消后不应继续重试，实际调用 %d 次", primary.calls)
	}
}

// TestRetry_HeuristicStringParse：err.Error() 含 "429" 但非 *HTTPError，也应识别为 429
func TestRetry_HeuristicStringParse(t *testing.T) {
	rawErr := errors.New("upstream returned: 429 Too Many Requests")
	primary := &testGen{tag: "primary", seq: []error{rawErr, nil}}
	g := WithRetry(primary, nil, noSleep())
	_, err := g.Generate(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("启发式解析 429 后应重试成功，实际 err=%v", err)
	}
	if primary.calls != 2 {
		t.Errorf("应重试 1 次，实际调用 %d", primary.calls)
	}
}

// TestHTTPError_ErrorString：基本 sanity，便于错误链 unwrap
func TestHTTPError_ErrorString(t *testing.T) {
	e := &HTTPError{Code: 429, Inner: errors.New("rate limit hit")}
	if !strings.Contains(e.Error(), "429") {
		t.Errorf("Error() 应含状态码，实际 %q", e.Error())
	}
	var got *HTTPError
	if !errors.As(e, &got) {
		t.Errorf("HTTPError 应可被 errors.As 抓出")
	}
	if got.Code != 429 {
		t.Errorf("As 后 Code 应为 429，实际 %d", got.Code)
	}
}

// netTimeoutErr 构造一个 net.Error 且 Timeout()==true。
func netTimeoutErr() error {
	return &timeoutErr{}
}

type timeoutErr struct{}

func (*timeoutErr) Error() string   { return "i/o timeout" }
func (*timeoutErr) Timeout() bool   { return true }
func (*timeoutErr) Temporary() bool { return true }

// 编译期断言：timeoutErr 满足 net.Error。
var _ net.Error = (*timeoutErr)(nil)
