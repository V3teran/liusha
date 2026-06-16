// Package toolinvocation 实现 tool_invocation 表的 model + store。
//
// 业务定位：记录每次 ReAct 工具调用的 telemetry——哪个 tool 跑了 / 参数 / 耗时 /
// 输出大小 / 错误。从 llm_invocation.result jsonb 里分离独立成表，让"sqlmap 跑了几次 /
// 平均耗时 / 成功率"这类聚合查询直接 SQL 可达，不必扫 jsonb。
//
// 写路径：internal/einoagent/tool_recorder.go 在每次工具调用前后埋点 + Append。
// 读路径：cmd/api viewer 按 (owner_type, owner_id) 拉本次扫描的所有 tool 调用展示。
package toolinvocation

import (
	"encoding/json"
	"time"
)

// Invocation 是 tool_invocation 表行的 Go 表示。
//
// OutputSize 是字节数；OutputPreview 是首 4096 字符的文本预览（避免存满 PG）。
// 完整 output 仍在 LLM message history（llm_invocation.messages jsonb）里。
type Invocation struct {
	ID            int64
	HunterID      string // FK→hunter.id（列名仍 hunter_id 是历史包袱）
	OwnerType     string // 'passive_session' / 'active_scan'
	OwnerID       string
	ToolName      string          // 'sqlmap' / 'curl' / 'write_finding' / ...
	Args          json.RawMessage // 工具调用参数 jsonb
	OutputSize    int             // 字节数
	OutputPreview string          // 首 4096 字符预览
	DurationMs    int             // Execute 耗时
	ErrorMessage  string          // Execute 错误时填；成功为空
	Done          bool            // Result.Done 信号——终止 ReAct 循环的 finding 提交
	CreatedAt     time.Time
}
