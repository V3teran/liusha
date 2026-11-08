// lifecycle.go 四 Agent 的启停生命周期（pause/resume 与主流程复用）。

package cognition

import (
	"context"
	"sync"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/logx"
)

// agentLifecycle 抽象四 agent 的启停：主流程收尾与暂停退出（停 agents 后结束
// 本 run）共用同一 Stop。Stop 必须（等待全部退出）后再返回，避免与 bus 的
// task 级共享通道关闭时序竞争。控制平面消费者与主流程会并发调用：互斥锁串行化，
// WaitGroup 的 Add/Wait 全部收在持锁区间内，规避 Add-after-Wait 误用。
type agentLifecycle struct {
	mu      sync.Mutex
	parent  context.Context
	logger  zerolog.Logger
	agents  []agentSpec
	wg      sync.WaitGroup
	cancel  context.CancelFunc
	running bool
	// onFatal 在 agent 非取消性退出时回调（agent 只在致命错误时主动返回）。
	onFatal func(name string, err error)
}

// agentSpec 是单个受管 agent 的启动入口。
type agentSpec struct {
	name  string
	start func(ctx context.Context) error
}

// newAgentLifecycle 构造生命周期管理器，管理给定的 agent 集。
func newAgentLifecycle(parent context.Context, logger zerolog.Logger, agents []agentSpec,
	onFatal func(name string, err error)) *agentLifecycle {
	return &agentLifecycle{parent: parent, logger: logger, agents: agents, onFatal: onFatal}
}

// Start 幂等启动全部 agent（已在运行时为 no-op）。
func (l *agentLifecycle) Start() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.running {
		return
	}
	agentCtx, cancel := context.WithCancel(l.parent)
	l.cancel = cancel
	l.running = true
	for _, ag := range l.agents {
		l.runAgent(agentCtx, ag)
	}
}

// runAgent 在 lifecycle 的 WaitGroup 下启动单个 agent goroutine（仅 Start 内调用）。
// 启动即注入 agent 身份：该 agent 全链路（含工具拦截器）可从 ctx 取到调用方归属。
func (l *agentLifecycle) runAgent(agentCtx context.Context, ag agentSpec) {
	l.wg.Add(1)
	logx.Go(l.logger, ag.name+"-agent", func() {
		defer l.wg.Done()
		if err := ag.start(WithAgent(agentCtx, ag.name)); err != nil && agentCtx.Err() == nil {
			l.logger.Error().Err(err).Msg(ag.name + " agent 异常退出")
			if l.onFatal != nil {
				l.onFatal(ag.name, err)
			}
		}
	})
}

// Stop 幂等停止：cancel 后等待全部 agent 退出。
func (l *agentLifecycle) Stop() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.running {
		return
	}
	l.cancel()
	l.wg.Wait()
	l.running = false
}
