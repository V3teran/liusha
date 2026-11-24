package runner

// heartbeat.go：run 的存活心跳。
//
// 恢复监控判定一轮 run 是否还在被推进，只认它的心跳（agent_run.heartbeat_at）距今有没有超过
// runner.run_recovery_timeout_seconds。心跳由持有这轮 run 的进程续约，并且只在「有新的操作
// 开始」或「有未到期的在途操作」时续约：
//   - 进程被硬杀：没有人续约；
//   - 进程活着但卡住了——既没有新的模型调用、工具调用开始，也没有在途的——同样停止续约。
//
// 一个有超时的长操作在途时（十几分钟的扫描、很慢的模型调用），心跳照常：那不是停摆。
// 只看「已完成的调用记录」做不到这一点——记录在操作结束时才写，长操作在途的整段时间里
// 是空白：实测一个要跑十分钟的命令，在单路执行时于第 6 分 44 秒被回收重投，从头再跑。
//
// 判定与处置只在恢复监控一处。这里只提供它的输入，不判定任何东西。

import (
	"context"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/agentrun"
	"github.com/V3teran/liusha/internal/framework/llm"
)

const (
	// heartbeatInterval 是续约的间隔：run 停摆之后至多这么久，监控才会看到下一次「没有续约」。
	heartbeatInterval = 30 * time.Second

	// startupGrace 是开跑后、第一次模型调用或工具调用之前允许的最长等待：等沙盒可达至多
	// sandboxWait，再留一个续约间隔。
	startupGrace = sandboxWait + heartbeatInterval
)

// Heartbeats 是本进程全部在途 run 的心跳登记处。零值不可用，经 NewHeartbeats 构造；
// 不装配（nil）时各方法都不做任何事。
type Heartbeats struct {
	mu   sync.Mutex
	runs map[string]*pulse
}

// pulse 是一轮 run 的心跳状态。
type pulse struct {
	// inFlight 是在途操作各自的截止时间：模型调用的调用截止时间、工具调用的超时。
	inFlight map[uint64]time.Time
	next     uint64
	// started 是自上次续约以来开始的操作数：有操作开始就是有进展。
	started uint64
	// grace 之前不算停摆：run 刚开跑、还没有任何操作登记。
	grace time.Time
	// quiet 记下是否已经停止续约，只在状态翻转时记一次日志。
	quiet bool
}

func NewHeartbeats() *Heartbeats {
	return &Heartbeats{runs: make(map[string]*pulse)}
}

// Attach 登记一轮开始执行的 run 并启动它的续约循环，返回停止函数（run 收尾时调用）。
func (h *Heartbeats) Attach(ctx context.Context, runs *agentrun.Store, runID string, logger zerolog.Logger) (stop func()) {
	if h == nil {
		return func() {}
	}
	h.mu.Lock()
	h.runs[runID] = &pulse{inFlight: make(map[uint64]time.Time), grace: time.Now().Add(startupGrace)}
	h.mu.Unlock()

	loopCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(heartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-loopCtx.Done():
				return
			case <-ticker.C:
				renew, wentQuiet := h.renew(runID)
				if wentQuiet {
					logger.Warn().Msg("run 既无进展也无在途操作：停止续约存活心跳，由恢复监控判定")
				}
				if !renew {
					continue
				}
				if err := runs.Heartbeat(loopCtx, runID); err != nil && loopCtx.Err() == nil {
					logger.Warn().Err(err).Msg("run 存活心跳续约失败")
				}
			}
		}
	}()

	return func() {
		cancel()
		h.mu.Lock()
		delete(h.runs, runID)
		h.mu.Unlock()
		<-done
	}
}

// renew 判断这一拍要不要续约：有未到期的在途操作，或自上一拍以来有操作开始，才续约。
// 返回是否续约，以及这一拍是否刚由续约转为停止。
func (h *Heartbeats) renew(runID string) (renew, wentQuiet bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	p := h.runs[runID]
	if p == nil {
		return false, false
	}
	now := time.Now()
	blocked := p.grace.After(now)
	for _, until := range p.inFlight {
		if until.After(now) {
			blocked = true
			break
		}
	}
	alive := blocked || p.started > 0
	p.started = 0
	if alive {
		p.quiet = false
		return true, false
	}
	if p.quiet {
		return false, false
	}
	p.quiet = true
	return false, true
}

// Busy 登记一轮 run 的一个在途操作，until 是它最晚结束的时刻；返回的撤销函数在操作结束时
// 调用，之后这个操作不再为心跳续约。不属于本进程任何在途 run 的调用（如经验沉淀）不做任何事。
func (h *Heartbeats) Busy(runID string, until time.Time) (release func()) {
	if h == nil {
		return func() {}
	}
	h.mu.Lock()
	p := h.runs[runID]
	if p == nil {
		h.mu.Unlock()
		return func() {}
	}
	id := p.next
	p.next++
	p.inFlight[id] = until
	p.started++
	h.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			h.mu.Lock()
			delete(p.inFlight, id)
			h.mu.Unlock()
		})
	}
}

// ModelCallChain 装配 runner 里模型调用的装饰链（由外到内）：审计 → 存活心跳登记 → 单步看门狗。
// 登记必须在看门狗之内：看门狗算出的截止时间才是这次调用真正的上限，登记的就是它——装反了
// 登记的是一个没有截止时间的值，慢的调用在途时 run 会被当成停摆。
func ModelCallChain(p llm.Provider, beats *Heartbeats, sink llm.UsageSink, stepLLM time.Duration, current func(context.Context) time.Duration) llm.Provider {
	return llm.InstrumentProvider(llm.WithDynamicCallTimeout(beats.TrackCalls(p), stepLLM, current), sink)
}

// TrackCalls 把每次模型调用登记为所属 run 的在途操作，调用在途期间心跳照常续约。
// 须包在单步看门狗之内：那里算出的截止时间才是这次调用真正的上限。
func (h *Heartbeats) TrackCalls(p llm.Provider) llm.Provider {
	if h == nil {
		return p
	}
	return &callTracker{inner: p, beats: h}
}

type callTracker struct {
	inner llm.Provider
	beats *Heartbeats
}

func (t *callTracker) ModelID() string    { return t.inner.ModelID() }
func (t *callTracker) ProviderID() string { return t.inner.ProviderID() }

func (t *callTracker) Complete(ctx context.Context, req llm.Request) (llm.Response, error) {
	until, _ := ctx.Deadline()
	release := t.beats.Busy(llm.CallMetaFromContext(ctx).AgentRunID, until)
	defer release()
	return t.inner.Complete(ctx, req)
}

// Stream 与 Complete 同口径：流结束（或被放弃）时撤销登记。
func (t *callTracker) Stream(ctx context.Context, req llm.Request) (<-chan llm.StreamEvent, error) {
	until, _ := ctx.Deadline()
	release := t.beats.Busy(llm.CallMetaFromContext(ctx).AgentRunID, until)
	ch, err := t.inner.Stream(ctx, req)
	if err != nil {
		release()
		return nil, err
	}
	out := make(chan llm.StreamEvent)
	go func() {
		defer close(out)
		defer release()
		for ev := range ch {
			select {
			case out <- ev:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}
