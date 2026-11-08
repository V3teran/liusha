package executor

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/explorationgraph"
)

// killPollInterval 是执行中轮询 action 是否被 monitor kill 的周期。
const killPollInterval = 3 * time.Second

// actionStateReader 是 kill 侦测所需的最小图读能力。
type actionStateReader interface {
	GetNode(ctx context.Context, id string) (*explorationgraph.Node, error)
}

// killSwitch 把 monitor 的 kill_action（图上 action → aborted）传导为执行侧的即时中断：
// 后台轮询 action 状态，发现 aborted 即置位并取消 action 级 context——在途的 LLM 生成与
// 工具调用随 context 立即退出，而不是等整轮迭代（可能含多个长工具）跑完才感知。
type killSwitch struct {
	killed atomic.Bool
	cancel context.CancelFunc
}

// watchKill 返回 action 级 context 与 killSwitch；调用方在执行结束时调用 stop 回收轮询。
func watchKill(parent context.Context, graph actionStateReader, actionID string, interval time.Duration, logger zerolog.Logger) (context.Context, *killSwitch, func()) {
	ctx, cancel := context.WithCancel(parent)
	ks := &killSwitch{cancel: cancel}
	if graph == nil {
		return ctx, ks, cancel
	}
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				node, err := graph.GetNode(ctx, actionID)
				if err != nil || node.State == nil || *node.State != explorationgraph.StateAborted {
					continue // 读失败按未中止处理：中止检查尽力而为，下一拍重试
				}
				reason := "unknown"
				if node.BlockedReason != nil {
					reason = *node.BlockedReason
				}
				logger.Warn().Str("action_id", actionID).Str("reason", reason).
					Msg("检测到 action 已被 monitor kill，取消在途执行")
				ks.killed.Store(true)
				cancel()
				return
			}
		}
	}()
	stop := func() {
		close(done)
		cancel()
	}
	return ctx, ks, stop
}

// Killed 报告 action 是否已被 kill。
func (k *killSwitch) Killed() bool { return k.killed.Load() }

// graphReader 返回 kill 侦测用的图读接口；未装配图时返回真 nil（避免 nil 指针装进
// 非 nil 接口，让 watchKill 误以为可轮询）。
func (e *Engine) graphReader() actionStateReader {
	if e.graph == nil {
		return nil
	}
	return e.graph
}
