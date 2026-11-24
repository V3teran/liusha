package llm

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// canQueue 返回一个预算宽裕的 ctx：调用至多排掉剩余预算的一半，要验证「最终排到」就得
// 给够预算。
func canQueue() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 10*time.Second)
}

// giveUpSoon 返回一个预算很小的 ctx：调用至多排掉预算的一半（约 80ms）就放弃。
func giveUpSoon() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 160*time.Millisecond)
}

func fastInflight(limit int) *inflight {
	i := newInflight(limit)
	i.maxWait = 80 * time.Millisecond
	return i
}

func queued(i *inflight, account string) int {
	i.mu.Lock()
	defer i.mu.Unlock()
	s := i.accounts[account]
	if s == nil {
		return 0
	}
	return s.queued()
}

// waitQueued 等到 account 上恰好有 n 个调用在排队。
func waitQueued(t *testing.T, i *inflight, account string, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for queued(i, account) < n {
		if time.Now().After(deadline) {
			t.Fatalf("第 %d 个调用没有排进队列", n)
		}
		time.Sleep(time.Millisecond)
	}
}

// awaitPermit 等到名额重新拿得到为止（归还发生在异步的转发协程里）。
func awaitPermit(t *testing.T, i *inflight, account string) func() {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		r, err := i.acquire(ctx, account, false)
		cancel()
		if err == nil {
			return r
		}
	}
	t.Fatal("名额迟迟没有归还")
	return nil
}

func assertBusy(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, ErrInflightBusy) {
		t.Fatalf("应因排不到在途名额而交还，实际 %v", err)
	}
	if Classify(err) != ClassBusy {
		t.Fatalf("排不到名额应归为本地的节流（ClassBusy），实际分类 %v", Classify(err))
	}
	// 对调用方它与限流同类：不是对这次动作的判决，稍后重来即可。
	if !IsTransient(err) {
		t.Fatal("排不到名额应视为瞬时故障")
	}
}

// 不限名额：立即全部拿到，归还函数重复调用也无害，不留下任何账户条目。
func TestInflight_Unlimited(t *testing.T) {
	i := fastInflight(0)
	var releases []func()
	for k := 0; k < 16; k++ {
		release, err := i.acquire(context.Background(), testAccount, false)
		if err != nil {
			t.Fatalf("不限名额时不应失败：%v", err)
		}
		releases = append(releases, release)
	}
	for _, release := range releases {
		release()
		release()
	}
	if n := len(i.accounts); n != 0 {
		t.Fatalf("不限名额时不应留下任何账户条目，实际 %d", n)
	}
}

// 同一账户同时在途的请求数不超过上限，超出的排队等前面的归还。
func TestInflight_CapsConcurrentRequests(t *testing.T) {
	const limit = 3
	i := fastInflight(limit)
	var active, peak atomic.Int32
	var wg sync.WaitGroup
	for k := 0; k < 24; k++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := canQueue()
			defer cancel()
			release, err := i.acquire(ctx, testAccount, false)
			if err != nil {
				t.Errorf("排队 5 秒应能拿到名额：%v", err)
				return
			}
			defer release()
			if n := active.Add(1); n > peak.Load() {
				peak.Store(n)
			}
			time.Sleep(5 * time.Millisecond)
			active.Add(-1)
		}()
	}
	wg.Wait()
	if p := peak.Load(); p > limit {
		t.Fatalf("同时在途 %d 个，超过上限 %d", p, limit)
	}
	if p := peak.Load(); p < 2 {
		t.Fatalf("应当出现过并发，否则这个用例没有验证到上限，峰值 %d", p)
	}
	if n := queued(i, testAccount); n != 0 || len(i.accounts) != 0 {
		t.Fatalf("全部归还后不应留下任何状态，排队 %d 个、账户条目 %d 个", n, len(i.accounts))
	}
}

// 名额按账户隔离：一个账户满了不约束另一个账户。
func TestInflight_IsPerAccount(t *testing.T) {
	i := fastInflight(1)
	releaseA, err := i.acquire(context.Background(), "account-a", false)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseA()
	ctx, cancel := giveUpSoon()
	defer cancel()

	_, err = i.acquire(ctx, "account-a", false)
	assertBusy(t, err)
	releaseB, err := i.acquire(ctx, "account-b", false)
	if err != nil {
		t.Fatalf("别的账户满了不应约束这个账户：%v", err)
	}
	releaseB()
}

// 先来先得：排队的调用按到达顺序拿到名额。
func TestInflight_FirstComeFirstServed(t *testing.T) {
	i := fastInflight(1)
	hold, _ := i.acquire(context.Background(), testAccount, false)

	var mu sync.Mutex
	var order []string
	start := func(name string) {
		go func() {
			ctx, cancel := canQueue()
			defer cancel()
			release, err := i.acquire(ctx, testAccount, false)
			if err != nil {
				t.Errorf("排队应最终拿到名额：%v", err)
				return
			}
			mu.Lock()
			order = append(order, name)
			mu.Unlock()
			release() // 立刻让给下一位
		}()
	}
	for k, name := range []string{"first", "second", "third"} {
		start(name)
		waitQueued(t, i, testAccount, k+1) // 确保按这个顺序排进队列
	}
	hold()

	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := len(order)
		mu.Unlock()
		if n == 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("排队的调用没有全部拿到名额，已拿到 %d 个", n)
		}
		time.Sleep(time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if order[0] != "first" || order[1] != "second" || order[2] != "third" {
		t.Fatalf("应按到达顺序放行，实际 %v", order)
	}
}

// 排满 maxWait 仍没排到：交还并说明原因；放弃的调用不在队列里，不占名额。
func TestInflight_GivesUpAfterMaxWait(t *testing.T) {
	i := fastInflight(1) // maxWait = 80ms
	hold, _ := i.acquire(context.Background(), testAccount, false)
	defer hold()

	start := time.Now()
	_, err := i.acquire(context.Background(), testAccount, false)
	assertBusy(t, err)
	if waited := time.Since(start); waited < 60*time.Millisecond || waited > 2*time.Second {
		t.Fatalf("应排队约 80ms 再放弃，实际等了 %v", waited)
	}
	if n := queued(i, testAccount); n != 0 {
		t.Fatalf("放弃后不应还留在队列里，实际 %d 个", n)
	}

	hold()
	release, err := i.acquire(context.Background(), testAccount, false)
	if err != nil {
		t.Fatalf("持有者归还后名额应当拿得到：%v", err)
	}
	release()
}

// 有空位就直接发：预算只约束排队，再小的预算也不挡本来就不需要等的请求。
func TestInflight_FreeSlotNeedsNoBudget(t *testing.T) {
	i := fastInflight(1)
	ctx, cancel := withBudget(20 * time.Millisecond)
	defer cancel()

	start := time.Now()
	release, err := i.acquire(ctx, testAccount, false)
	if err != nil {
		t.Fatalf("有空位时无论预算多小都应当立即拿到：%v", err)
	}
	if waited := time.Since(start); waited >= 10*time.Millisecond {
		t.Fatalf("有空位时不应有任何等待，实际等了 %v", waited)
	}
	release()
}

// 调用方的预算已经用尽：原因在调用方，原样交还它的错误，有没有空位都一样。报成
// 「名额已满」会让同一个原因得到两种错误——有空位时请求照发、以超时返回，没空位时却
// 成了账户满载，调用方对两者的处置可以不同（规划者只对前者放大下一轮预算）。
func TestInflight_ExhaustedBudgetIsReportedAsCallers(t *testing.T) {
	ended := []struct {
		name string
		ctx  func() (context.Context, context.CancelFunc)
		want error
	}{
		{"截止时间已过", func() (context.Context, context.CancelFunc) {
			return context.WithTimeout(context.Background(), -time.Second)
		}, context.DeadlineExceeded},
		{"已被取消", func() (context.Context, context.CancelFunc) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			return ctx, func() {}
		}, context.Canceled},
	}
	for _, tc := range ended {
		for _, full := range []bool{false, true} {
			name := tc.name + "/有空位"
			if full {
				name = tc.name + "/名额已满"
			}
			t.Run(name, func(t *testing.T) {
				i := newInflight(1) // maxWait 是一分钟：挡住它的只能是对调用方错误的如实交还
				if full {
					hold, _ := i.acquire(context.Background(), testAccount, false)
					defer hold()
				}
				ctx, cancel := tc.ctx()
				defer cancel()

				start := time.Now()
				release, err := i.acquire(ctx, testAccount, false)
				if !errors.Is(err, tc.want) || errors.Is(err, ErrInflightBusy) {
					t.Fatalf("应原样交还调用方的 %v，实际 %v", tc.want, err)
				}
				if release != nil {
					t.Fatal("没有发出请求，不应交归还函数")
				}
				if waited := time.Since(start); waited >= 50*time.Millisecond {
					t.Fatalf("预算已用尽的调用不应排队，实际等了 %v", waited)
				}
				if n := queued(i, testAccount); n != 0 {
					t.Fatalf("不应留在队列里，实际 %d 个", n)
				}
			})
		}
	}
}

// 排队至多用掉本次调用剩余预算的一半：另一半留给请求本身。
func TestInflight_WaitsAtMostHalfTheCallBudget(t *testing.T) {
	i := newInflight(1) // 没有截止时间时的上限是一分钟：这里只有预算的一半能约束它
	hold, _ := i.acquire(context.Background(), testAccount, false)
	defer hold()
	ctx, cancel := withBudget(100 * time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := i.acquire(ctx, testAccount, false)
	assertBusy(t, err)
	if waited := time.Since(start); waited < 40*time.Millisecond || waited > 90*time.Millisecond {
		t.Fatalf("应按预算的一半排队约 50ms 再放弃，实际等了 %v", waited)
	}
}

// 排队中调用方取消：立即以取消返回，名额不多不少。
func TestInflight_CancelWhileWaiting(t *testing.T) {
	i := newInflight(1) // maxWait 是一分钟：只有取消能让它返回
	hold, _ := i.acquire(context.Background(), testAccount, false)
	defer hold()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := i.acquire(ctx, testAccount, false); done <- err }()
	waitQueued(t, i, testAccount, 1)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("取消后应以取消返回，实际 %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("取消未能打断排队")
	}
	if n := queued(i, testAccount); n != 0 {
		t.Fatalf("取消后不应还留在队列里，实际 %d 个", n)
	}
}

// 归还函数重复调用不会多出名额。
func TestInflight_ReleaserReturnsOnce(t *testing.T) {
	i := fastInflight(1)
	release, _ := i.acquire(context.Background(), testAccount, false)
	release()
	release()

	hold, _ := i.acquire(context.Background(), testAccount, false)
	defer hold()
	ctx, cancel := giveUpSoon()
	defer cancel()
	_, err := i.acquire(ctx, testAccount, false)
	assertBusy(t, err)
}

// enqueueWaiter 直接往 account 的队列里放一个等待者（不经 acquire），返回它——用来确定性地
// 摆出「放行与放弃谁先谁后」。
func enqueueWaiter(t *testing.T, i *inflight, account string) *inflightWaiter {
	t.Helper()
	i.mu.Lock()
	defer i.mu.Unlock()
	s := i.accounts[account]
	if s == nil || s.active < i.limit {
		t.Fatal("要先占满名额，等待者才会排队")
	}
	w := &inflightWaiter{granted: make(chan struct{})}
	w.queue = &s.ordinary
	w.elem = w.queue.PushBack(w)
	return w
}

// 放弃发生在放行之前：等待者从队列里撤出，名额不多不少。
func TestInflight_GiveUpBeforeGrantWithdraws(t *testing.T) {
	i := fastInflight(1)
	hold, _ := i.acquire(context.Background(), testAccount, false)
	defer hold()
	w := enqueueWaiter(t, i, testAccount)

	cause := errors.New("等不下去了")
	release, err := i.giveUp(testAccount, w, cause)
	if !errors.Is(err, cause) || release != nil {
		t.Fatalf("仍在队列里时应撤出并交还原因、不交回归还函数，实际 交回了归还函数=%v err=%v", release != nil, err)
	}
	if n := queued(i, testAccount); n != 0 {
		t.Fatalf("撤出后不应还留在队列里，实际 %d 个", n)
	}
}

// 放弃发生在放行之后：名额已经转到等待者名下，撤不出来——必须把它的归还函数交出去，
// 否则这个名额就永远丢了。
func TestInflight_GiveUpAfterGrantHandsBackPermit(t *testing.T) {
	i := fastInflight(1)
	hold, _ := i.acquire(context.Background(), testAccount, false)
	w := enqueueWaiter(t, i, testAccount)
	hold() // 名额直接转给等待者
	<-w.granted

	cause := errors.New("等不下去了")
	release, err := i.giveUp(testAccount, w, cause)
	if err != nil || release == nil {
		t.Fatalf("已被放行时应交回它的归还函数且不带错误，实际 交回了归还函数=%v err=%v", release != nil, err)
	}
	// 交出去之前名额是满的；归还之后必须能重新拿到。
	ctx, cancel := giveUpSoon()
	defer cancel()
	_, err = i.acquire(ctx, testAccount, false)
	assertBusy(t, err)
	release()
	probe, err := i.acquire(context.Background(), testAccount, false)
	if err != nil {
		t.Fatalf("归还后名额应当拿得到：%v", err)
	}
	probe()
}

// 真实的并发下放行与放弃怎样撞车都行：跑很多轮，每一轮结束名额都不丢。
func TestInflight_NoPermitLostWhenGiveUpRacesWithGrant(t *testing.T) {
	i := newInflight(1)
	i.maxWait = 2 * time.Millisecond
	for round := 0; round < 400; round++ {
		hold, _ := i.acquire(context.Background(), testAccount, false)
		done := make(chan func(), 1)
		go func() {
			ctx, cancel := canQueue()
			defer cancel()
			if r, err := i.acquire(ctx, testAccount, false); err == nil {
				done <- r
			} else {
				done <- nil
			}
		}()
		// 让持有者的归还落在等待者放弃的前后附近：各轮错开，两种先后顺序都会出现。
		time.Sleep(time.Duration(round%5) * 900 * time.Microsecond)
		hold()
		if r := <-done; r != nil {
			r()
		}
		probe, err := i.acquire(context.Background(), testAccount, false)
		if err != nil {
			t.Fatalf("第 %d 轮后名额丢失：%v", round, err)
		}
		probe()
	}
}

// ── 重试装饰器与在途名额 ───────────────────────────────────────────────────

func inflightFor(inner Provider, i *inflight) Provider {
	return WithRetry(inner, retryOptsForTest(), &governor{cooldown: newCooldown(), inflight: i}, testAccount)
}

// 排不到名额的调用立即收场：不盲试、不发请求，以「没发出去」的身份交还。
func TestInflightBusyEndsCallWithoutSending(t *testing.T) {
	i := fastInflight(1)
	hold, _ := i.acquire(context.Background(), testAccount, false)
	defer hold()
	inner := &scriptedProvider{outcomes: []error{nil}}
	p := inflightFor(inner, i)
	ctx, cancel := giveUpSoon()
	defer cancel()

	start := time.Now()
	_, err := p.Complete(ctx, Request{})
	assertBusy(t, err)
	if waited := time.Since(start); waited < 60*time.Millisecond {
		t.Fatalf("应当先排队等名额（约 80ms），实际只等了 %v", waited)
	}
	if inner.sent() != 0 {
		t.Fatalf("没排到名额不应发出请求，实际发出 %d 次", inner.sent())
	}
	if !isHeld(err) {
		t.Fatalf("没发出去的调用应带「被挡下」标记（审计不留提示词），实际 %v", err)
	}
}

// 重试途中排不到名额：到此为止，不再按退避表空转。
func TestInflightBusyInsideRetryLoopEndsCall(t *testing.T) {
	i := fastInflight(1)
	grabbed := make(chan func(), 1)
	inner := &scriptedProvider{outcomes: []error{&HTTPError{Code: 503}, nil}}
	inner.hook = func(int) {
		// 第一次请求在途时，让另一个调用方排进队列：这次请求归还的名额会直接转给它，
		// 紧接着的重试那一次就排不到了。
		go func() {
			ctx, cancel := canQueue()
			defer cancel()
			if r, err := i.acquire(ctx, testAccount, false); err == nil {
				grabbed <- r
			}
		}()
		waitQueued(t, i, testAccount, 1)
	}
	p := inflightFor(inner, i)
	ctx, cancel := withBudget(400 * time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := p.Complete(ctx, Request{})
	assertBusy(t, err)
	// 「到此为止」也指时间：排不到就收场，不按退避表再排一轮、再等一轮。
	if waited := time.Since(start); waited >= 260*time.Millisecond {
		t.Fatalf("排不到名额应立即收场（只等一轮，约预算的一半），实际 %v", waited)
	}
	if inner.sent() != 1 {
		t.Fatalf("排不到名额后不应再发请求，应只发出第一次，实际 %d 次", inner.sent())
	}
	// 这次调用真的发过请求：不标记为「没发出去」，第一次请求的失败现场照常留在审计里。
	if isHeld(err) {
		t.Fatalf("发过请求的调用不应带「被挡下」标记：%v", err)
	}
	(<-grabbed)()
}

// inFlightCountingProvider 在请求真实在途期间计数（排队等待不算在途）。
type inFlightCountingProvider struct {
	active, peak atomic.Int32
}

func (p *inFlightCountingProvider) Complete(context.Context, Request) (Response, error) {
	if n := p.active.Add(1); n > p.peak.Load() {
		p.peak.Store(n)
	}
	time.Sleep(5 * time.Millisecond)
	p.active.Add(-1)
	return Response{}, nil
}

func (p *inFlightCountingProvider) Stream(context.Context, Request) (<-chan StreamEvent, error) {
	return nil, ErrStreamingUnsupported
}

func (p *inFlightCountingProvider) ModelID() string    { return "m" }
func (p *inFlightCountingProvider) ProviderID() string { return "p" }

// 同一账户的各 provider 共用一份名额：两个 provider 实例加起来的在途请求数不超过上限。
func TestInflight_SharedAcrossProvidersOfOneAccount(t *testing.T) {
	const limit = 2
	i := fastInflight(limit)
	g := &governor{cooldown: newCooldown(), inflight: i}
	inner := &inFlightCountingProvider{}
	var wg sync.WaitGroup
	for k := 0; k < 2; k++ { // 两个 provider 实例（如同一把密钥下的两个模型）
		p := WithRetry(inner, retryOptsForTest(), g, testAccount)
		for n := 0; n < 6; n++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ctx, cancel := canQueue()
				defer cancel()
				if _, err := p.Complete(ctx, Request{}); err != nil {
					t.Errorf("排队应最终拿到名额：%v", err)
				}
			}()
		}
	}
	wg.Wait()
	if p := inner.peak.Load(); p > limit {
		t.Fatalf("两个实例合计同时在途 %d 个，超过上限 %d", p, limit)
	}
}

// gatedStreamProvider 的流由测试控制何时结束；ctx 取消时随之关闭（与真实生成器同口径）。
type gatedStreamProvider struct {
	started chan (<-chan StreamEvent)
	finish  chan struct{}
}

func (p *gatedStreamProvider) Complete(context.Context, Request) (Response, error) {
	return Response{}, errors.New("本用例只走流式")
}

func (p *gatedStreamProvider) Stream(ctx context.Context, _ Request) (<-chan StreamEvent, error) {
	ch := make(chan StreamEvent)
	go func() {
		select {
		case <-ctx.Done():
		case <-p.finish:
		}
		close(ch)
	}()
	p.started <- ch
	return ch, nil
}

func (p *gatedStreamProvider) ModelID() string    { return "m" }
func (p *gatedStreamProvider) ProviderID() string { return "p" }

// 流式请求在流结束前一直占着名额，流结束后归还。
func TestInflightHeldUntilStreamEnds(t *testing.T) {
	i := fastInflight(1)
	inner := &gatedStreamProvider{started: make(chan (<-chan StreamEvent), 1), finish: make(chan struct{})}
	p := inflightFor(inner, i)
	ctx, cancel := canQueue()
	defer cancel()

	out, err := p.Stream(ctx, Request{})
	if err != nil {
		t.Fatalf("流应建立：%v", err)
	}
	<-inner.started // 流已建立：请求在途

	ctx2, cancel2 := giveUpSoon()
	defer cancel2()
	_, err = i.acquire(ctx2, testAccount, false)
	assertBusy(t, err) // 流还没结束，名额被占着

	close(inner.finish) // 流结束
	for range out {
	}
	awaitPermit(t, i, testAccount)()
}

// 消费方放弃（ctx 取消）时名额也归还得回去：底层流随 ctx 关闭。
func TestInflightReleasedWhenStreamAbandoned(t *testing.T) {
	i := fastInflight(1)
	inner := &gatedStreamProvider{started: make(chan (<-chan StreamEvent), 1), finish: make(chan struct{})}
	p := inflightFor(inner, i)
	ctx, cancel := canQueue()

	out, err := p.Stream(ctx, Request{})
	if err != nil {
		t.Fatalf("流应建立：%v", err)
	}
	<-inner.started
	cancel() // 消费方放弃，一个事件都没读
	awaitPermit(t, i, testAccount)()
	for range out { // 转发通道随之关闭
	}
}

// ── 优先的调用 ─────────────────────────────────────────────────────────────

func enqueuePriorityWaiter(t *testing.T, i *inflight, account string) *inflightWaiter {
	t.Helper()
	i.mu.Lock()
	defer i.mu.Unlock()
	s := i.accounts[account]
	if s == nil || s.active < i.limit {
		t.Fatal("要先占满名额，等待者才会排队")
	}
	w := &inflightWaiter{granted: make(chan struct{})}
	w.queue = &s.priority
	w.elem = w.queue.PushBack(w)
	return w
}

// awaitGrant 等一个等待者被放行，至多 2 秒。
func awaitGrant(t *testing.T, w *inflightWaiter, who string) {
	t.Helper()
	select {
	case <-w.granted:
	case <-time.After(2 * time.Second):
		t.Fatalf("%s 没有被放行", who)
	}
}

// 优先的调用排在普通调用之前：普通的先到也让位。
func TestInflight_PriorityJumpsAheadOfOrdinaryWaiters(t *testing.T) {
	i := fastInflight(1)
	hold, _ := i.acquire(context.Background(), testAccount, false)
	enqueueWaiter(t, i, testAccount) // 普通的先到
	enqueueWaiter(t, i, testAccount)
	urgent := enqueuePriorityWaiter(t, i, testAccount)

	hold()
	awaitGrant(t, urgent, "优先的调用")
	if n := queued(i, testAccount); n != 2 {
		t.Fatalf("两个普通的调用应仍在排队，实际 %d 个", n)
	}
}

// 优先的调用之间同样先来先得。
func TestInflight_PriorityWaitersAreFirstComeFirstServed(t *testing.T) {
	i := fastInflight(2)
	holdA, _ := i.acquire(context.Background(), testAccount, false)
	holdB, _ := i.acquire(context.Background(), testAccount, false)
	first := enqueuePriorityWaiter(t, i, testAccount)
	second := enqueuePriorityWaiter(t, i, testAccount)

	holdA() // 只空出一个名额
	awaitGrant(t, first, "先到的优先调用")
	select {
	case <-second.granted:
		t.Fatal("只空出一个名额时，后到的不该被放行")
	default:
	}
	holdB()
	awaitGrant(t, second, "后到的优先调用")
}

// 优先的调用放弃时从优先队列撤出，不影响排队的普通调用。
func TestInflight_PriorityGivesUpCleanly(t *testing.T) {
	i := fastInflight(1)
	hold, _ := i.acquire(context.Background(), testAccount, false)
	defer hold()
	enqueueWaiter(t, i, testAccount)
	urgent := enqueuePriorityWaiter(t, i, testAccount)

	cause := errors.New("等不下去了")
	release, err := i.giveUp(testAccount, urgent, cause)
	if !errors.Is(err, cause) || release != nil {
		t.Fatalf("仍在队列里时应撤出并交还原因，实际 交回了归还函数=%v err=%v", release != nil, err)
	}
	i.mu.Lock()
	s := i.accounts[testAccount]
	priorityLeft, ordinaryLeft := s.priority.Len(), s.ordinary.Len()
	i.mu.Unlock()
	if priorityLeft != 0 || ordinaryLeft != 1 {
		t.Fatalf("撤出后应只剩那个普通的调用，实际 优先 %d 个、普通 %d 个", priorityLeft, ordinaryLeft)
	}
}

// 优先标记穿过重试层生效：普通调用先到，名额空出时先发优先的那个。
func TestPriorityRequestIsServedBeforeOrdinary(t *testing.T) {
	i := fastInflight(1)
	g := &governor{cooldown: newCooldown(), inflight: i}
	hold, _ := i.acquire(context.Background(), testAccount, false)

	var mu sync.Mutex
	var order []string
	named := func(name string) Provider {
		p := &scriptedProvider{outcomes: []error{nil}}
		p.hook = func(int) {
			mu.Lock()
			order = append(order, name)
			mu.Unlock()
		}
		return p
	}
	ordinary := WithRetry(named("ordinary"), retryOptsForTest(), g, testAccount)
	urgent := WithRetry(named("priority"), retryOptsForTest(), g, testAccount)

	done := make(chan error, 2)
	ctxA, cancelA := canQueue()
	defer cancelA()
	go func() { _, err := ordinary.Complete(ctxA, Request{}); done <- err }()
	waitQueued(t, i, testAccount, 1)
	ctxB, cancelB := canQueue()
	defer cancelB()
	go func() { _, err := urgent.Complete(ctxB, Request{Priority: true}); done <- err }()
	waitQueued(t, i, testAccount, 2)

	hold()
	for k := 0; k < 2; k++ {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("排队应最终拿到名额：%v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("排队的调用没有全部完成")
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(order) != 2 || order[0] != "priority" || order[1] != "ordinary" {
		t.Fatalf("应先发优先的请求，实际发送顺序 %v", order)
	}
}

// 优先标记决定调用排进哪条队列：带标记的进优先队列，不进普通队列。
func TestInflight_AcquireRoutesPriorityToItsQueue(t *testing.T) {
	i := fastInflight(1)
	hold, _ := i.acquire(context.Background(), testAccount, false)
	defer hold()

	go func() {
		ctx, cancel := canQueue()
		defer cancel()
		if r, err := i.acquire(ctx, testAccount, true); err == nil {
			r()
		}
	}()
	waitQueued(t, i, testAccount, 1)
	i.mu.Lock()
	s := i.accounts[testAccount]
	priorityLen, ordinaryLen := s.priority.Len(), s.ordinary.Len()
	i.mu.Unlock()
	if priorityLen != 1 || ordinaryLen != 0 {
		t.Fatalf("优先的调用应排在优先队列，实际 优先 %d 个、普通 %d 个", priorityLen, ordinaryLen)
	}
}
