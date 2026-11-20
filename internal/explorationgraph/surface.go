package explorationgraph

import (
	"context"
	"fmt"

	"github.com/V3teran/liusha/internal/framework/core"
)

// Surface 是某一时刻所见的图面：依据的根目标版本与按类计数。节点只增不删、状态只进
// 不退，两个 Surface 相等即这段时间图面没有长出新内容。规划者据它复用「本轮不再新增
// 动作」的结论，也据它判断别处发来的重规划请求是否已经过时。
type Surface struct {
	Goal         int64 // 根目标版本：目标改写、任务状态迁移都会推进它
	Objectives   int
	Actions      int
	Settled      int // 已结束的动作
	Results      int
	Observations int
	Refuted      int // 已证伪的假设
}

// Surface 读取任务图面。目标版本取自 basis 而不是另读一次：调用方据 basis 得出的结论
// 与这份图面同属一个时刻——两次读取之间目标恰好被改写时，结论不会被记到新目标名下。
func (s *Store) Surface(ctx context.Context, taskID string, basis GoalBasis) (Surface, error) {
	nodes, err := s.ListNodesByTask(ctx, taskID, "")
	if err != nil {
		return Surface{}, fmt.Errorf("读取图面: %w", err)
	}
	v := Surface{Goal: basis.Version}
	for i := range nodes {
		n := &nodes[i]
		switch {
		case n.IsAction():
			v.Actions++
			if n.State != nil && n.State.IsTerminal() {
				v.Settled++
			}
		case n.IsResult():
			v.Results++
		case n.IsObservation():
			v.Observations++
			if n.HasVerificationStatus(VerificationRefuted) {
				v.Refuted++
			}
		case n.Kind == core.KindObjective:
			v.Objectives++
		}
	}
	return v, nil
}
