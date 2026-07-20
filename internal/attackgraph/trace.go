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
	Result    string // tool_result 的结果预览（write_finding 含 {"id":...}）
}

// toolWriteFinding 是写漏洞工具名；其 tool_result 返回 {"id":<finding-id>}，用于把成果链挂到思维链。
const toolWriteFinding = "write_finding"

// ThinkingChain 从会话事件流（message KindEvent，按 seq 序入参）派生思维链节点 + flow 骨干边。
//
//   - reasoning   → 想节点（Title=推理首行）
//   - tool_call   → 做+得节点（开），后续同名 tool_result 回填 done/error 状态
//   - tool_result 无配对 tool_call → 独立做+得节点
//   - spawn       → agent 边界节点
//
// flow 边按节点创建顺序串成线性骨干（树 / 死路在后续切片派生）。
// 原文不入节点（见设计 §6）：节点只带 Title + Ref（指向 message id，点开取原文）。
func ThinkingChain(messages []conversation.Message) ([]Node, []Edge, map[string]string) {
	var nodes []Node
	openAction := map[string]int{}       // toolName → 未闭合 action 节点在 nodes 的下标
	findingParent := map[string]string{} // finding id → 产出它的 write_finding 动作节点 id（成果链挂思维链）

	// 树重建：首个 agent 为主线（orchestrator），spawn 派生子代理分支。
	primary := ""                      // 主线 agent
	lastPrimary := ""                  // 主线最后节点 id（含 spawn）
	lastByAgent := map[string]string{} // agent → 该 agent 链最后节点 id
	spawnFor := map[string]string{}    // subagent_type → spawn 节点 id

	// addNode 按 agent 归属算父节点、入列、更新 last 指针。spawn 与主线节点归主线。
	addNode := func(n Node, agent string, isSpawn bool) {
		var parent string
		switch {
		case isSpawn || agent == primary || agent == "":
			parent = lastPrimary
		default: // 子代理节点
			if last, ok := lastByAgent[agent]; ok {
				parent = last // 同 agent 链内顺接
			} else if sp, ok := spawnFor[agent]; ok {
				parent = sp // 子代理首个节点挂到派它的 spawn 下
			} else {
				parent = lastPrimary
			}
		}
		n.ParentID = parent
		n.Agent = agent
		nodes = append(nodes, n)
		if isSpawn || agent == primary || agent == "" {
			lastPrimary = n.ID
		} else {
			lastByAgent[agent] = n.ID
		}
	}

	for _, m := range messages {
		if m.Kind != conversation.KindEvent || len(m.Metadata) == 0 {
			continue
		}
		var ev traceEvent
		if err := json.Unmarshal(m.Metadata, &ev); err != nil {
			continue // 脏 metadata 跳过，不阻断整图
		}
		agent := ev.AgentName
		if primary == "" && agent != "" {
			primary = agent
		}

		switch ev.Kind {
		case evReasoning:
			addNode(Node{ID: m.ID, Kind: KindReasoning, Title: firstLine(ev.Text, reasoningTitleMax), Ref: m.ID}, agent, false)
		case evSpawn:
			st := subagentType(ev.Args)
			delete(lastByAgent, st) // 重新派生该类型 → 新分支从此 spawn 起
			addNode(Node{ID: m.ID, Kind: KindAgent, Title: spawnTitle(ev.Args), Ref: m.ID}, agent, true)
			if st != "" {
				spawnFor[st] = m.ID
			}
		case evToolCall:
			addNode(Node{ID: m.ID, Kind: KindAction, Title: "调用 " + ev.ToolName, Ref: m.ID, Status: "done"}, agent, false)
			openAction[ev.ToolName] = len(nodes) - 1
		case evToolResult:
			if idx, ok := openAction[ev.ToolName]; ok {
				if ev.Err != "" {
					nodes[idx].Status = "error" // 配对 tool_call 翻 error（死路信号）
				}
				nodes[idx].Ref = m.ID // 钻取指向结果消息（看工具输出，而非调用入参）
				// write_finding 的结果含 {"id":...}：记下该 finding 由这个动作节点产出，供成果链挂接。
				if ev.ToolName == toolWriteFinding {
					if fid := findingIDFromResult(ev.Result); fid != "" {
						findingParent[fid] = nodes[idx].ID
					}
				}
				delete(openAction, ev.ToolName)
			} else {
				st := "done"
				if ev.Err != "" {
					st = "error"
				}
				addNode(Node{ID: m.ID, Kind: KindAction, Title: "结果 " + ev.ToolName, Ref: m.ID, Status: st}, agent, false)
				// 未配对（write_finding 调用交错导致）也捕获 finding id，挂到本结果节点，避免漏连。
				if ev.ToolName == toolWriteFinding {
					if fid := findingIDFromResult(ev.Result); fid != "" {
						findingParent[fid] = m.ID
					}
				}
			}
		}
	}

	// 边：父 → 子（思维链骨干树；无父的为根）
	var edges []Edge
	for _, n := range nodes {
		if n.ParentID != "" {
			edges = append(edges, Edge{From: n.ParentID, To: n.ID, Type: EdgeFlow})
		}
	}
	return nodes, edges, findingParent
}

// findingIDFromResult 从 write_finding 的 tool_result（{"id":"..."}）取 finding id；失败返空。
func findingIDFromResult(result string) string {
	var r struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(result), &r); err != nil {
		return ""
	}
	return r.ID
}

// subagentType 从 task 工具入参 {subagent_type} 取子代理类型；解析失败/缺字段返回空。
func subagentType(args string) string {
	var in struct {
		SubagentType string `json:"subagent_type"`
	}
	if err := json.Unmarshal([]byte(args), &in); err != nil {
		return ""
	}
	return in.SubagentType
}

// spawnTitle 拼派发短标签；缺类型兜底"派发子代理"（完整在 message）。
func spawnTitle(args string) string {
	if st := subagentType(args); st != "" {
		return "派发 " + st
	}
	return "派发子代理"
}
