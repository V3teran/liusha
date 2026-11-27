package planner

// ResultAnalysis 是 planner 对新 Result 的分析结论：提出新的探索方向。方向只作分组，由
// 分析追加、不在规划层收敛。动作只由常规规划排出——它看得到事实、已做与在途的动作，
// 分析的视野里没有这些。
type ResultAnalysis struct {
	NewObjectives []NewObjective // 发现的新探索方向
	Reasoning     string         // 判断理由
}

// NewObjective 表示结果分析从新 Result 中提取的探索方向：既是分析响应里的条目，也是分析
// 结论的载体（方向只作分组，不带优先级）。
type NewObjective struct {
	Description string   `json:"description"`  // 方向描述
	TriggeredBy []string `json:"triggered_by"` // 催生它的 Result ID 列表
	Reasoning   string   `json:"reasoning"`    // 为什么需要这个方向
}
