package executor

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/explorationgraph"
)

// ownershipPollInterval 是执行中核对「本 run 是否仍持有该动作」的周期。
const ownershipPollInterval = 3 * time.Second

// ErrActionRevoked 表示动作在执行途中被收回：被否决、随任务收口而中止，或被新 run
// 回收后重新认领。收回是外部决定，不是执行失败——执行方放弃本次结果，不改写动作状态。
var ErrActionRevoked = errors.New("动作已被收回")

// actionStateReader 是持有侦测所需的最小图读能力。
type actionStateReader interface {
	GetNode(ctx context.Context, id string) (*explorationgraph.Node, error)
}

// ownership 把「动作被收回」传导为执行侧的即时中断：后台轮询动作，发现本 run 已不再持有
// （不在 running，或已由别的 run 认领）即置位并取消 action 级 context——在途的 LLM
// 生成与工具调用随 context 立即退出，而不是等整轮迭代（可能含多个长工具）跑完才感知。
type ownership struct {
	lost   atomic.Bool
	cancel context.CancelFunc
}

// holds 判断 runID 是否仍持有该动作。
func holds(node *explorationgraph.Node, runID string) bool {
	return node.State != nil && *node.State == explorationgraph.StateRunning && node.ClaimedBy() == runID
}

// revocation 描述动作为何不再由 runID 持有（仍持有返回 nil）。
func revocation(node *explorationgraph.Node, runID string) error {
	if holds(node, runID) {
		return nil
	}
	state := "unknown"
	if node.State != nil {
		state = string(*node.State)
	}
	reason := ""
	if node.StateReason != nil {
		reason = *node.StateReason
	}
	return fmt.Errorf("%w：状态 %s，认领者 %q，原因 %q", ErrActionRevoked, state, node.ClaimedBy(), reason)
}

// watchOwnership 返回 action 级 context 与持有状态；调用方在执行结束时调用 stop 回收轮询。
// 读图失败按仍持有处理：侦测尽力而为，下一拍重试。
func watchOwnership(parent context.Context, graph actionStateReader, actionID, runID string, interval time.Duration, logger zerolog.Logger) (context.Context, *ownership, func()) {
	ctx, cancel := context.WithCancel(parent)
	own := &ownership{cancel: cancel}
	if graph == nil {
		return ctx, own, cancel
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
				if err != nil {
					continue
				}
				if why := revocation(node, runID); why != nil {
					logger.Warn().Str("action_id", actionID).Err(why).Msg("动作已被收回，取消在途执行")
					own.lost.Store(true)
					cancel()
					return
				}
			}
		}
	}()
	stop := func() {
		close(done)
		cancel()
	}
	return ctx, own, stop
}

// Lost 报告动作是否已被收回。
func (o *ownership) Lost() bool { return o.lost.Load() }

// graphReader 返回持有侦测用的图读接口；未装配图时返回真 nil（避免 nil 指针装进
// 非 nil 接口，让 watchOwnership 误以为可轮询）。
func (e *Engine) graphReader() actionStateReader {
	if e.graph == nil {
		return nil
	}
	return e.graph
}
