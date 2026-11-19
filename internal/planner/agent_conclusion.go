package planner

import (
	"context"
	"fmt"

	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
)

// graphView 是一次规划所见的图面：按类计数。节点只增不删、状态只进不退，计数不变即
// 图面没有长出新内容——规划者对它得出的结论无需重新求解。
type graphView struct {
	goal         int64 // 根目标版本：目标改写、任务状态迁移都会推进它
	objectives   int
	actions      int
	settled      int // 已结束的动作
	results      int
	observations int
	refuted      int // 已证伪的假设
}

// conclusion 是规划者对某个图面「本轮不再新增动作」的结论，在图面不变时复用，避免对
// 同一局面反复付费求解——反复求解既浪费，也会让结论在同一事实上来回翻转。
//
// 两种结论的失效口径不同：
//   - 停止探索针对「任务还要不要继续」：只有任务目标被改写，或图上出现了判停时没有的
//     工作与发现（新目标、新动作、新结果）才失效。收回的动作留下的观察与验证结论是
//     判停时已放弃的那部分探索的余波，不重开规划；
//   - 等待（继续但本轮没有新动作）针对「当前图面上还能做什么」：图面任何进展（动作
//     结束、新观察、新的验证结论、新结果）都使它失效。
type conclusion struct {
	stop bool
	view graphView
}

// holds 判断结论对当前图面是否仍然成立（nil 表示没有结论）。
func (c *conclusion) holds(now graphView) bool {
	if c == nil {
		return false
	}
	if !c.stop {
		return c.view == now
	}
	return c.view.goal == now.goal && c.view.objectives == now.objectives &&
		c.view.actions == now.actions && c.view.results == now.results
}

// observeView 读取当前图面（目标版本取自规划依据，与依据同一时刻读取）。
func (a *Agent) observeView(ctx context.Context, basis explorationgraph.GoalBasis) (graphView, error) {
	nodes, err := a.graph.ListNodesByTask(ctx, a.taskID, "")
	if err != nil {
		return graphView{}, fmt.Errorf("读取图面: %w", err)
	}
	v := graphView{goal: basis.Version}
	for i := range nodes {
		n := &nodes[i]
		switch {
		case n.IsAction():
			v.actions++
			if n.State != nil && n.State.IsTerminal() {
				v.settled++
			}
		case n.IsResult():
			v.results++
		case n.IsObservation():
			v.observations++
			if n.HasVerificationStatus(explorationgraph.VerificationRefuted) {
				v.refuted++
			}
		case n.Kind == core.KindObjective:
			v.objectives++
		}
	}
	return v, nil
}
