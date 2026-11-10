package planner

import (
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
)

// ResultAnalysis 是 planner 对新 Result 的分析结论：派生目标集合的修订
// （宣告达成 / 提出新方向并吸收旧方向）与任务方向上的延续动作。
type ResultAnalysis struct {
	CompletedObjectives []explorationgraph.ObjectiveCompletion // 已达成的派生目标（附作证 Result）
	NewObjectives       []NewObjective                         // 发现的新探索方向
	ContinuationActions []ContinuationAction                   // 当前方向的延续
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

// ContinuationAction 表示在任务方向上继续深挖的新动作
type ContinuationAction struct {
	Instruction string        // 动作指令
	Priority    core.Priority // 优先级
	TriggeredBy []string      // 触发此动作的 Result ID 列表
	Reasoning   string        // 为什么需要这个动作
}

// forcePlanActions 强制重规划（跳过 executable action 检查）
// 用于 Monitor 请求重规划的场景（探索停滞、资源耗尽等）
