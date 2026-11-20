package planner

import "github.com/V3teran/liusha/internal/explorationgraph"

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
	view explorationgraph.Surface
}

// holds 判断结论对当前图面是否仍然成立（nil 表示没有结论）。
func (c *conclusion) holds(now explorationgraph.Surface) bool {
	if c == nil {
		return false
	}
	if !c.stop {
		return c.view == now
	}
	return c.view.Goal == now.Goal && c.view.Objectives == now.Objectives &&
		c.view.Actions == now.Actions && c.view.Results == now.Results
}
