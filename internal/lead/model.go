// Package lead 实现情报黑板（见 spec §7）：一次交战内、需跨 agent / 跨 run 同步的过程情报。
//
// 定位是"跨 agent 情报黑板"——子代理看不到 planner 会话，靠这块共享黑板同步过程情报
// （agent 自己的 scratchpad 已由会话 reasoning 事件承担，lead 不重复那份）。
//
// 共享轴 = host（与 credential / lesson 同轴），不挂 assignment/task：情报本质"关于目标"，
// 不"关于任务"——同 host 不管流量何时来、属哪个 task，情报自动汇一起。
//
// 存储用 Redis（不是 PG）：高频写 + 高频读 + 滚动过期，Redis 的 LTRIM/EXPIRE 天然承担淘汰。
package lead

import "time"

// Kind 是情报能触发的 agent 动作分类——按动作分（最少且穷尽），不按主题分。
type Kind string

const (
	KindClue        Kind = "clue"        // 可疑点 → 去验证
	KindObservation Kind = "observation" // 既成发现 → 记住并利用
	KindDeadend     Kind = "deadend"     // 死路 → 绕开别试
)

// normalizeKind 把历史 Redis 值 "fact" 兜底映射到 KindObservation（当年 Fact→Observation
// 反抄袭改名的读侧兼容，杜绝旧数据失配）。
func normalizeKind(k Kind) Kind {
	if k == "fact" {
		return KindObservation
	}
	return k
}

// Entry 是一条情报。
//
// SourceTaskID：哪次 task 发现的（溯源，前端展示"来自哪次扫描"）。
// lead(observation) 与 finding 的边界：finding = 有 evidence、可复现 repro_cmd、进交付报告的漏洞；
// lead(observation) = 尚未坐实成 PoC 的认知/观察。升级路径：验证成 PoC → 写 finding；lead 不删，靠淘汰消失。
type Entry struct {
	Kind         Kind      `json:"kind"`
	Detail       string    `json:"detail"` // 一句人话，位置/细节都在这里说清
	ExecutorID     string    `json:"agent_id"`
	SourceTaskID string    `json:"source_task_id"`
	CreatedAt    time.Time `json:"created_at"`
}
