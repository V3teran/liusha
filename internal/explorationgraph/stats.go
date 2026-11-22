package explorationgraph

import (
	"context"
	"fmt"
)

// TaskStats 是任务图的计数摘要：run 结局日志的统计口径，也是动作预算的计数口径。
type TaskStats struct {
	ExecutedActions int // 执行过的动作（被认领过即算，不论结局）
	Results         int // 坐实的结果
}

// Stats 统计任务图。从图上计数而不是累计事件：跨 run（暂停后继续、恢复重投）也
// 是同一个口径，不会因进程重启或事件丢失而失真。
func (s *Store) Stats(ctx context.Context, taskID string) (TaskStats, error) {
	nodes, err := s.ListNodesByTask(ctx, taskID, "")
	if err != nil {
		return TaskStats{}, fmt.Errorf("统计任务图: %w", err)
	}
	var st TaskStats
	for i := range nodes {
		n := &nodes[i]
		switch {
		case n.IsAction() && n.ClaimedBy() != "":
			st.ExecutedActions++
		case n.IsResult():
			st.Results++
		}
	}
	return st, nil
}
