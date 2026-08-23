// Package scanagent 定义 Agent 过程事件类型。
//
// 与 actor/dispatcher 层解耦：event 是纯值类型，不引入任何业务包。
// runner 实现 EventSink，把事件落 conversation message + 广播 redis。
package scanagent

import "context"

// ScanEventKind 区分过程事件类型。
type ScanEventKind string

const (
	// ScanEventToolCall：agent 发起一次工具调用（执行前发）。
	ScanEventToolCall ScanEventKind = "tool_call"
	// ScanEventToolResult：工具返回结果（执行后发）。
	ScanEventToolResult ScanEventKind = "tool_result"
	// ScanEventReasoning：每轮 LLM 调用后产出的推理文字（思路/分析/计划/决策）。
	ScanEventReasoning ScanEventKind = "reasoning"
	// ScanEventReasoningDelta：流式推理增量片段，仅 publish redis 不落库。
	ScanEventReasoningDelta ScanEventKind = "reasoning_delta"
	// ScanEventSpawn：planner 派子代理（active swarm 场景）。
	ScanEventSpawn ScanEventKind = "spawn"
	// ScanEventCompaction：上下文压缩发生，Text 为蒸馏摘要。
	ScanEventCompaction ScanEventKind = "compaction"
	// ScanEventInsight：agent 主动标记关键判断/发现。
	ScanEventInsight ScanEventKind = "insight"
)

// ScanEvent 是 agent 运行中的一条过程事件。
type ScanEvent struct {
	Kind       ScanEventKind
	ToolName   string // 工具名
	CallID     string // 工具调用唯一 id，tool_call/tool_result 配对键
	Args       string // tool_call 的入参（JSON 文本）
	Result     string // tool_result 的结果（截断预览）
	DurationMs int    // tool_result 的执行耗时（ms）
	Err        string // 工具执行错误（如有）
	Text       string // reasoning/compaction 的文字内容
	AgentName  string // 产出该事件的 agent 名
	InTokens   int    // reasoning：本次 LLM 输入 token
	OutTokens  int    // reasoning：本次 LLM 输出 token
	LatencyMs  int    // reasoning：本次 LLM 耗时（ms）
}

// EventSink 消费 agent 过程事件。runner 注入具体实现。
type EventSink interface {
	OnScanEvent(ctx context.Context, ev ScanEvent)
}
