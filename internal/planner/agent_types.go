package planner

import (
	"github.com/V3teran/liusha/internal/framework/core"
)

// ResultAnalysis 是 planner 对新 Result 的分析结论：提出新的探索方向。方向只作分组，由
// 分析追加、不在规划层收敛。动作只由常规规划排出——它看得到事实、已做与在途的动作，
// 分析的视野里没有这些。
type ResultAnalysis struct {
	NewObjectives []NewObjective // 发现的新探索方向
	Reasoning     string         // 判断理由
}

// NewObjective 表示从 Result 中提取的新探索方向
type NewObjective struct {
	Description string        // 方向描述
	Priority    core.Priority // 优先级
	TriggeredBy []string      // 催生它的 Result ID 列表
	Reasoning   string        // 为什么需要这个方向
}
