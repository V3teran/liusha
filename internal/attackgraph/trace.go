package attackgraph

import (
	"encoding/json"

	"github.com/V3teran/liusha/internal/conversation"
)

const reasoningTitleMax = 120

// 事件 Kind 字面量（对齐 internal/einoagent.ScanEventKind）。
const (
	evReasoning  = "reasoning"
	evToolCall   = "tool_call"
	evToolResult = "tool_result"
	evSpawn      = "spawn"
)

// traceEvent 是 message.Metadata 里 einoagent.ScanEvent 的最小可解析镜像。
//
// ScanEvent 无 json tag，默认按字段名（首字母大写）序列化；这里字段名对齐，
// json.Unmarshal 大小写不敏感故可匹配。真相源：internal/einoagent/scan_event.go。
// 刻意不 import einoagent（避免把 eino 重依赖拖进纯投影包）。
type traceEvent struct {
	Kind      string
	ToolName  string
	Args      string
	Text      string
	AgentName string
	Err       string
}

// ThinkingChain 从对话事件流（message KindEvent，按 seq 序入参）派生思维链节点 + flow 骨干边。
//
//   - reasoning   → 想节点（Title=推理首行）
//   - tool_call   → 做+得节点（开），后续同名 tool_result 回填 done/error 状态
//   - tool_result 无配对 tool_call → 独立做+得节点
//   - spawn       → agent 边界节点
//
// flow 边按节点创建顺序串成线性骨干（树 / 死路在后续切片派生）。
// 原文不入节点（见设计 §6）：节点只带 Title + Ref（指向 message id，点开取原文）。
func ThinkingChain(messages []conversation.Message) ([]Node, []Edge) {
	var nodes []Node
	openAction := map[string]int{} // toolName → 未闭合 action 节点在 nodes 的下标

	for _, m := range messages {
		if m.Kind != conversation.KindEvent || len(m.Metadata) == 0 {
			continue
		}
		var ev traceEvent
		if err := json.Unmarshal(m.Metadata, &ev); err != nil {
			continue // 脏 metadata 跳过，不阻断整图
		}

		switch ev.Kind {
		case evReasoning:
			nodes = append(nodes, Node{
				ID:    m.ID,
				Kind:  KindReasoning,
				Title: firstLine(ev.Text, reasoningTitleMax),
				Ref:   m.ID,
			})
		case evSpawn:
			nodes = append(nodes, Node{
				ID:    m.ID,
				Kind:  KindAgent,
				Title: spawnTitle(ev.Args),
				Ref:   m.ID,
			})
		case evToolCall:
			nodes = append(nodes, Node{
				ID:     m.ID,
				Kind:   KindAction,
				Title:  "调用 " + ev.ToolName,
				Ref:    m.ID,
				Status: "done", // 默认 done，配对的 tool_result 若带 Err 再翻 error
			})
			openAction[ev.ToolName] = len(nodes) - 1
		case evToolResult:
			if idx, ok := openAction[ev.ToolName]; ok {
				if ev.Err != "" {
					nodes[idx].Status = "error"
				}
				delete(openAction, ev.ToolName)
			} else {
				st := "done"
				if ev.Err != "" {
					st = "error"
				}
				nodes = append(nodes, Node{
					ID:     m.ID,
					Kind:   KindAction,
					Title:  "结果 " + ev.ToolName,
					Ref:    m.ID,
					Status: st,
				})
			}
		}
	}

	var edges []Edge
	for i := 1; i < len(nodes); i++ {
		edges = append(edges, Edge{From: nodes[i-1].ID, To: nodes[i].ID, Type: EdgeFlow})
	}
	return nodes, edges
}

// spawnTitle 从 task 工具入参 {subagent_type, description} 拼派发标签。
// 解析失败或缺字段兜底"派发子代理"（content 仅作短标签，完整在 message）。
func spawnTitle(args string) string {
	var in struct {
		SubagentType string `json:"subagent_type"`
	}
	if err := json.Unmarshal([]byte(args), &in); err != nil || in.SubagentType == "" {
		return "派发子代理"
	}
	return "派发 " + in.SubagentType
}
