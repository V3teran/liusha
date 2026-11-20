package planner

import (
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
)

// ResultAnalysis 是 planner 对新 Result 的分析结论：只修订派生目标集合（宣告达成 /
// 提出新方向并吸收旧方向）。动作只由常规规划排出——它看得到事实、已做与在途的动作，
// 分析的视野里没有这些。
type ResultAnalysis struct {
	CompletedObjectives []explorationgraph.ObjectiveCompletion // 已达成的派生目标（附作证 Result）
	NewObjectives       []NewObjective                         // 发现的新探索方向
	Reasoning           string                                 // 判断理由
}

// NewObjective 表示从 Result 中提取的新探索目标
type NewObjective struct {
	Description string        // 目标描述
	Priority    core.Priority // 优先级
	TriggeredBy []string      // 触发此目标的 Result ID 列表
	Supersedes  []string      // 被本目标吸收的既有派生目标 ID 列表
	Reasoning   string        // 为什么需要这个目标
}
