package runner

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/framework/llm"
)

// 让一轮 run 的开跑宽限期失效：此后只有操作能让它续约。
func expireGrace(t *testing.T, h *Heartbeats, runID string) {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	h.runs[runID].grace = time.Now().Add(-time.Second)
}

// inFlight 是一轮 run 当前登记着的在途操作数。
func (h *Heartbeats) inFlight(runID string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	if p := h.runs[runID]; p != nil {
		return len(p.inFlight)
	}
	return 0
}

// latestDeadline 是一轮 run 当前登记着的在途操作里最晚的截止时间（没有登记为零值）。
func (h *Heartbeats) latestDeadline(runID string) time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	var latest time.Time
	if p := h.runs[runID]; p != nil {
		for _, until := range p.inFlight {
			if until.After(latest) {
				latest = until
			}
		}
	}
	return latest
}

// 续约的条件只有两个：有未到期的在途操作，或自上一拍以来有操作开始（操作开始本身就是
// 进展）。两者都没有即停止续约，并且只在停止的那一刻报告一次。
func TestHeartbeats_RenewsOnlyWhileThereIsWork(t *testing.T) {
	h := NewHeartbeats()
	defer h.Attach(context.Background(), nil, "run-1", zerolog.Nop())()

	if renew, _ := h.renew("run-1"); !renew {
		t.Fatal("刚开跑、宽限期内应续约")
	}
	expireGrace(t, h, "run-1")
	if renew, quiet := h.renew("run-1"); renew || !quiet {
		t.Fatalf("宽限期已过且没有操作：应停止续约并报告一次，实际续约=%v 报告=%v", renew, quiet)
	}
	if _, quiet := h.renew("run-1"); quiet {
		t.Fatal("持续停摆不应重复报告")
	}

	// 已经过期的截止时间不算在途；但操作开始过，下一拍续约一次。
	expired := h.Busy("run-1", time.Now().Add(-time.Second))
	if renew, _ := h.renew("run-1"); !renew {
		t.Fatal("有操作开始过，应续约一次")
	}
	if renew, _ := h.renew("run-1"); renew {
		t.Fatal("唯一的操作已过期，不应续约")
	}
	expired()

	// 未到期的在途操作：每一拍都续约，撤销之后不再算。
	long := h.Busy("run-1", time.Now().Add(time.Hour))
	for i := 0; i < 3; i++ {
		if renew, _ := h.renew("run-1"); !renew {
			t.Fatalf("有未到期的在途操作，第 %d 拍应续约", i+1)
		}
	}
	long()
	if renew, quiet := h.renew("run-1"); renew || !quiet {
		t.Fatalf("在途操作已结束且无新操作：应停止续约并报告，实际续约=%v 报告=%v", renew, quiet)
	}
}

// 不属于本进程任何在途 run 的调用（如经验沉淀）不做任何事；run 收尾之后它的登记随之清掉。
func TestHeartbeats_IgnoresUnknownAndFinishedRuns(t *testing.T) {
	h := NewHeartbeats()
	h.Busy("nobody", time.Now().Add(time.Hour))()
	if renew, quiet := h.renew("nobody"); renew || quiet {
		t.Fatalf("没有登记过的 run 不应续约也不应报告，实际 %v %v", renew, quiet)
	}

	stop := h.Attach(context.Background(), nil, "run-1", zerolog.Nop())
	release := h.Busy("run-1", time.Now().Add(time.Hour))
	if got := h.inFlight("run-1"); got != 1 {
		t.Fatalf("在途操作应登记 1 个，实际 %d", got)
	}
	stop()
	if got := h.inFlight("run-1"); got != 0 {
		t.Fatalf("run 结束后登记应清掉，实际 %d", got)
	}
	h.Busy("run-1", time.Now().Add(time.Hour))()
	release()
	if got := h.inFlight("run-1"); got != 0 {
		t.Fatalf("已结束的 run 不应再被登记，实际 %d", got)
	}
}

// 撤销函数重复调用不会多撤销别人的登记。
func TestHeartbeats_ReleaseIsIdempotent(t *testing.T) {
	h := NewHeartbeats()
	defer h.Attach(context.Background(), nil, "run-1", zerolog.Nop())()
	first := h.Busy("run-1", time.Now().Add(time.Hour))
	second := h.Busy("run-1", time.Now().Add(time.Hour))
	first()
	first()
	if got := h.inFlight("run-1"); got != 1 {
		t.Fatalf("重复撤销不应影响另一个在途操作，实际剩 %d 个", got)
	}
	second()
	if got := h.inFlight("run-1"); got != 0 {
		t.Fatalf("全部撤销后不应剩登记，实际剩 %d 个", got)
	}
}

// 判定阈值的下界要容得下三次续约，开跑宽限期要盖得住等沙盒可达的上限：这两层关系断了，
// 健康运行的 run 就会被当成僵尸。
func TestHeartbeatTimingFitsItsAssumptions(t *testing.T) {
	if floor := time.Duration(config.MinRunRecoveryTimeoutSeconds) * time.Second; floor < 3*heartbeatInterval {
		t.Fatalf("配置允许的最小判定阈值 %v 容不下三次续约（%v）", floor, 3*heartbeatInterval)
	}
	if startupGrace <= sandboxWait {
		t.Fatalf("开跑宽限期 %v 盖不住等沙盒可达的上限 %v", startupGrace, sandboxWait)
	}
}

// 记录一次调用在途时登记着几个操作、最晚截止时间是什么的 provider。
type observingProvider struct {
	beats    *Heartbeats
	run      string
	observed atomic.Int32
	deadline atomic.Int64
}

func (p *observingProvider) Complete(ctx context.Context, _ llm.Request) (llm.Response, error) {
	p.observe(ctx)
	return llm.Response{}, nil
}

func (p *observingProvider) Stream(ctx context.Context, _ llm.Request) (<-chan llm.StreamEvent, error) {
	p.observe(ctx)
	ch := make(chan llm.StreamEvent, 1)
	ch <- llm.StreamEvent{Kind: llm.StreamDone}
	close(ch)
	return ch, nil
}

func (p *observingProvider) ModelID() string    { return "m" }
func (p *observingProvider) ProviderID() string { return "p" }

// observe 记下这次调用在途时的登记：数量，以及登记的截止时间是否就是调用自己的截止时间。
func (p *observingProvider) observe(ctx context.Context) {
	p.observed.Store(int32(p.beats.inFlight(p.run)))
	want, _ := ctx.Deadline()
	p.deadline.Store(boolToInt64(p.beats.latestDeadline(p.run).Equal(want)))
}

func boolToInt64(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// 每次模型调用在途期间都登记为所属 run 的在途操作，调用结束即撤销；流式到流结束。
// 不属于任何 run 的调用不登记。
func TestHeartbeats_TrackCalls(t *testing.T) {
	h := NewHeartbeats()
	defer h.Attach(context.Background(), nil, "run-1", zerolog.Nop())()
	inner := &observingProvider{beats: h, run: "run-1"}
	p := h.TrackCalls(inner)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	ctx = llm.WithCallMeta(ctx, llm.CallMeta{TaskID: "t", AgentRunID: "run-1", Role: "executor"})

	if _, err := p.Complete(ctx, llm.Request{}); err != nil {
		t.Fatal(err)
	}
	if got := inner.observed.Load(); got != 1 {
		t.Fatalf("非流式调用在途时应登记 1 个操作，实际 %d", got)
	}
	if got := inner.deadline.Load(); got != 1 {
		t.Fatal("登记的截止时间应是这次调用自己的截止时间：登记得偏短，慢的调用在途时 run 会被当成停摆")
	}
	if got := h.inFlight("run-1"); got != 0 {
		t.Fatalf("调用结束后应撤销登记，实际剩 %d", got)
	}

	ch, err := p.Stream(ctx, llm.Request{})
	if err != nil {
		t.Fatal(err)
	}
	for range ch { //nolint:revive // 读到流结束
	}
	if got := inner.observed.Load(); got != 1 {
		t.Fatalf("流式调用在途时应登记 1 个操作，实际 %d", got)
	}
	if got := inner.deadline.Load(); got != 1 {
		t.Fatal("流式调用登记的截止时间应是这次调用自己的截止时间")
	}
	if got := h.inFlight("run-1"); got != 0 {
		t.Fatalf("流结束后应撤销登记，实际剩 %d", got)
	}

	// 不属于任何 run 的调用：不登记，也不出错。
	if _, err := p.Complete(context.Background(), llm.Request{}); err != nil {
		t.Fatal(err)
	}
	if got := inner.observed.Load(); got != 0 {
		t.Fatalf("不属于 run 的调用不应登记，实际 %d", got)
	}
}

// 建流即败：登记当场撤销，不留到 nobody 去清理。
func TestHeartbeats_TrackCallsReleasesWhenStreamFailsToStart(t *testing.T) {
	h := NewHeartbeats()
	defer h.Attach(context.Background(), nil, "run-1", zerolog.Nop())()
	p := h.TrackCalls(&failingStreamProvider{beats: h, run: "run-1"})
	ctx := llm.WithCallMeta(context.Background(), llm.CallMeta{AgentRunID: "run-1"})

	if _, err := p.Stream(ctx, llm.Request{}); err == nil {
		t.Fatal("建流失败应原样交还错误")
	}
	if got := h.inFlight("run-1"); got != 0 {
		t.Fatalf("建流失败后不应留着登记，实际剩 %d", got)
	}
}

type failingStreamProvider struct {
	beats *Heartbeats
	run   string
}

func (p *failingStreamProvider) Complete(context.Context, llm.Request) (llm.Response, error) {
	return llm.Response{}, nil
}

func (p *failingStreamProvider) Stream(context.Context, llm.Request) (<-chan llm.StreamEvent, error) {
	if got := p.beats.inFlight(p.run); got != 1 {
		return nil, fmt.Errorf("建流时应有 1 个登记，实际 %d", got)
	}
	return nil, errors.New("上游不可用")
}

func (p *failingStreamProvider) ModelID() string    { return "m" }
func (p *failingStreamProvider) ProviderID() string { return "p" }

// 没装配心跳登记处（nil）时一切照常：不续约、不登记、调用链原样返回。
func TestHeartbeats_NilIsInert(t *testing.T) {
	var none *Heartbeats
	none.Busy("run-1", time.Now().Add(time.Hour))()
	none.Attach(context.Background(), nil, "run-1", zerolog.Nop())()
	inner := &observingProvider{}
	if got := none.TrackCalls(inner); got != llm.Provider(inner) {
		t.Fatal("nil 登记处不应包装调用链")
	}
}

// deadlineObserver 记下调用在途时它自己的截止时间与心跳登记的截止时间。
type deadlineObserver struct {
	beats *Heartbeats
	run   string
	saw   atomic.Int64 // 调用自己的截止时间距调用开始多久（毫秒）
	match atomic.Int32 // 心跳登记的截止时间是否与调用自己的截止时间一致
}

func (o *deadlineObserver) Complete(ctx context.Context, _ llm.Request) (llm.Response, error) {
	start := time.Now()
	deadline, _ := ctx.Deadline()
	o.saw.Store(time.Until(deadline).Milliseconds() + time.Since(start).Milliseconds())
	o.match.Store(int32(boolToInt64(o.beats.latestDeadline(o.run).Equal(deadline))))
	return llm.Response{}, nil
}

func (o *deadlineObserver) Stream(context.Context, llm.Request) (<-chan llm.StreamEvent, error) {
	return nil, errors.New("not used")
}

func (o *deadlineObserver) ModelID() string    { return "m" }
func (o *deadlineObserver) ProviderID() string { return "p" }

// 装配出的调用链：登记给存活心跳的截止时间，就是单步看门狗给这次调用定的上限（热配置优先，
// 其次部署值；调用方自己给的截止时间更早时取更早的）。
func TestModelCallChain_RegistersTheWatchdogDeadline(t *testing.T) {
	h := NewHeartbeats()
	defer h.Attach(context.Background(), nil, "run-1", zerolog.Nop())()
	inner := &deadlineObserver{beats: h, run: "run-1"}
	runCtx := llm.WithCallMeta(context.Background(), llm.CallMeta{AgentRunID: "run-1"})
	cases := []struct {
		name     string
		hot      time.Duration // 热配置给出的上限；0 = 读不到，回落部署值
		deploy   time.Duration
		parentIn time.Duration // 调用方自己给的截止时间；0 = 不给
		want     time.Duration
	}{
		{"读不到热配置时用部署值", 0, 5 * time.Minute, 0, 5 * time.Minute},
		{"热配置优先", 40 * time.Second, 5 * time.Minute, 0, 40 * time.Second},
		{"调用方的截止时间更早时取更早的", 40 * time.Second, 5 * time.Minute, 12 * time.Second, 12 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := ModelCallChain(inner, h, nil, tc.deploy, func(context.Context) time.Duration { return tc.hot })
			ctx, cancel := runCtx, context.CancelFunc(func() {})
			if tc.parentIn > 0 {
				ctx, cancel = context.WithTimeout(runCtx, tc.parentIn)
			}
			defer cancel()
			if _, err := p.Complete(ctx, llm.Request{}); err != nil {
				t.Fatal(err)
			}
			if saw := time.Duration(inner.saw.Load()) * time.Millisecond; saw > tc.want || tc.want-saw > 5*time.Second {
				t.Fatalf("调用的截止时间应约为 %v 之后，实际 %v", tc.want, saw)
			}
			if inner.match.Load() != 1 {
				t.Fatal("登记给存活心跳的截止时间应与调用自己的截止时间一致")
			}
		})
	}
}
