package planner

import (
	"github.com/V3teran/liusha/internal/framework/core"
)

type ResultAnalysis struct {
	Completed           bool                 // 当前 Objective 是否已完成
	Evidence            []string             // 支撑完成判断的证据（Result IDs）
	NewObjectives       []NewObjective       // 发现的新探索方向
	ContinuationActions []ContinuationAction // 当前方向的延续
	Reasoning           string               // 判断理由
}

// NewObjective 表示从 Result 中提取的新探索目标
type NewObjective struct {
	Description string        // 目标描述
	Priority    core.Priority // 优先级
	TriggeredBy []string      // 触发此目标的 Result ID 列表
	Reasoning   string        // 为什么需要这个目标
}

// ContinuationAction 表示在当前 Objective 下继续探索的新动作
type ContinuationAction struct {
	Instruction string        // 动作指令
	Priority    core.Priority // 优先级
	TriggeredBy []string      // 触发此动作的 Result ID 列表
	Reasoning   string        // 为什么需要这个动作
}

// forcePlanActions 强制重规划（跳过 executable action 检查）
// 用于 Monitor 请求重规划的场景（探索停滞、资源耗尽等）
