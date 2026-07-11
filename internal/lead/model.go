// Package lead 实现情报黑板（见 spec §7）：一次交战内、需跨 agent / 跨 run 同步的过程情报。
//
// 补的是 note 退役后未被平替的那半——note 的"agent 自己 scratchpad"已被对话 reasoning 事件
// 正确平替；"跨 agent 情报黑板"没被平替（子代理看不到对话），lead 只承担后者。
//
// 共享轴 = host（与 credential / lesson 同轴），不挂 assignment/task：情报本质"关于目标"，
// 不"关于任务"——同 host 不管流量何时来、属哪个 task，情报自动汇一起。
//
// 存储用 Redis（不是 PG）：高频写 + 高频读 + 会过期，特性同当年的 note。
package lead

import "time"

// Kind 是情报能触发的 agent 动作分类——按动作分（最少且穷尽），不按主题分。
type Kind string

const (
	KindClue    Kind = "clue"    // 可疑点 → 去验证
	KindFact    Kind = "fact"    // 既成发现 → 记住并利用
	KindDeadend Kind = "deadend" // 死路 → 绕开别试
)

// Entry 是一条情报。
//
// SourceTaskID：哪次 task 发现的（溯源，前端展示"来自哪次扫描"）。
// lead(fact) 与 finding 的边界：finding = 有 evidence、可复现 repro_cmd、进交付报告的漏洞；
// lead(fact) = 尚未坐实成 PoC 的认知/观察。升级路径：验证成 PoC → 写 finding；lead 不删，靠淘汰消失。
type Entry struct {
	Kind         Kind      `json:"kind"`
	Note         string    `json:"note"`
	HunterID     string    `json:"hunter_id"`
	SourceTaskID string    `json:"source_task_id"`
	CreatedAt    time.Time `json:"created_at"`
}
