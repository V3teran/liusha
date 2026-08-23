package cognition

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/V3teran/liusha/internal/planner"
)

// Report 是一次认知环运行的收尾摘要（供 L6 交付/审计）。
type Report struct {
	Steps    int    // 实际执行的招法步数
	Promoted int    // 成功晋升进图的节点数
	Attempts int    // Executor 产出的候选晋升总数
	StopWhy  string // 终止原因：frontier-exhausted / max-steps / ctx-canceled
}

const (
	stopExhausted = "frontier-exhausted"
	stopMaxSteps  = "max-steps"
	stopCanceled  = "ctx-canceled"
)

// Run 驱动 taskID 的认知环直到 frontier 耗尽、触顶或 ctx 取消。
//
// 每轮：Plan → 取排序里首个「本轮未试过」的招法 → Executor 执行产候选 → 逐个过
// Verifier 晋升 → 重新 Plan。取首个未试而非恒取 moves[0]：没产出晋升的招法若不
// 记忆，重规划会反复吐同一条形成空转；已试集让环跳过它、推进到次高优，晋升产生的新节点
// 因身份不同（Kind+OnNodeID）仍会被拾起。终止 = 无未试招法（frontier 实质耗尽）。
func (l *Loop) Run(ctx context.Context, taskID string) (Report, error) {
	if taskID == "" {
		return Report{}, fmt.Errorf("cognition: taskID 为空")
	}
	var rep Report
	tried := map[moveID]bool{} // 本次运行已试过的招法身份
	for {
		if err := ctx.Err(); err != nil {
			rep.StopWhy = stopCanceled
			return rep, err
		}
		if rep.Steps >= l.maxSteps {
			rep.StopWhy = stopMaxSteps
			return rep, nil
		}

		moves, err := l.planner.Plan(ctx, taskID)
		if err != nil {
			return rep, fmt.Errorf("cognition: 规划失败: %w", err)
		}
		next, ok := firstUntried(moves, tried)
		if !ok {
			rep.StopWhy = stopExhausted
			return rep, nil
		}

		tried[idOf(next)] = true
		if err := l.step(ctx, next, &rep); err != nil {
			return rep, err
		}
		rep.Steps++
	}
}

// moveID 是招法的稳定身份，用于「已试」去重。
type moveID struct {
	kind   planner.MoveKind
	onNode string
}

func idOf(m planner.Move) moveID {
	return moveID{kind: m.Kind, onNode: m.OnNodeID}
}

func firstUntried(moves []planner.Move, tried map[moveID]bool) (planner.Move, bool) {
	for _, m := range moves {
		if !tried[idOf(m)] {
			return m, true
		}
	}
	return planner.Move{}, false
}

// step 执行单条招法：Executor 产候选 → 逐个过晋升门。
// Executor 执行失败不中断全环（一条路走不通不该拖垮整次交战），记因由后返回让上层重规划。
func (l *Loop) step(ctx context.Context, m planner.Move, rep *Report) error {
	attempts, err := l.executor.Execute(ctx, m)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		// 战术失败是常态，非环错误：不晋升、不增 promoted，交回上层重规划。
		return nil
	}
	for _, a := range attempts {
		rep.Attempts++
		node, err := l.promoter.Promote(ctx, a)
		if err != nil {
			return fmt.Errorf("cognition: 晋升失败: %w", err)
		}
		if node != nil {
			rep.Promoted++

			// 发布验证通过事件（节点成功晋升到世界模型）
			if l.eventBus != nil && node.TaskID != "" {
				if nodeID, parseErr := uuid.Parse(node.ID); parseErr == nil {
					l.eventBus.PublishVerificationPassed(node.TaskID, nodeID)
				}
			}
		}
	}
	return nil
}
