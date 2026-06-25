package attackgraph

import (
	"encoding/json"
	"testing"

	"github.com/V3teran/liusha/internal/conversation"
	"github.com/V3teran/liusha/internal/finding"
)

// mkEventMsg 造一条 KindEvent 消息，Metadata = ScanEvent 风格 JSON（字段名匹配，大小写不敏感）。
func mkEventMsg(id, kind string, fields map[string]any) conversation.Message {
	if fields == nil {
		fields = map[string]any{}
	}
	fields["Kind"] = kind
	b, _ := json.Marshal(fields)
	return conversation.Message{ID: id, Kind: conversation.KindEvent, Metadata: b}
}

func TestThinkingChain(t *testing.T) {
	t.Run("空与非事件消息忽略", func(t *testing.T) {
		msgs := []conversation.Message{
			{ID: "m1", Kind: conversation.KindMessage, Content: "用户问句"},
		}
		nodes, edges, _ := ThinkingChain(msgs)
		if len(nodes) != 0 || len(edges) != 0 {
			t.Fatalf("期望空图，得 %d 节点 %d 边", len(nodes), len(edges))
		}
	})

	t.Run("单 reasoning 出一个想节点", func(t *testing.T) {
		msgs := []conversation.Message{
			mkEventMsg("m1", evReasoning, map[string]any{"Text": "先扫目录\n细节"}),
		}
		nodes, edges, _ := ThinkingChain(msgs)
		if len(nodes) != 1 || len(edges) != 0 {
			t.Fatalf("期望 1 节点 0 边，得 %d/%d", len(nodes), len(edges))
		}
		if nodes[0].Kind != KindReasoning || nodes[0].Title != "先扫目录" || nodes[0].Ref != "m1" {
			t.Errorf("想节点字段不符：%+v", nodes[0])
		}
	})

	t.Run("tool_call+tool_result 配对成一个动作节点", func(t *testing.T) {
		msgs := []conversation.Message{
			mkEventMsg("m1", evReasoning, map[string]any{"Text": "试试上传"}),
			mkEventMsg("m2", evToolCall, map[string]any{"ToolName": "curl"}),
			mkEventMsg("m3", evToolResult, map[string]any{"ToolName": "curl"}),
		}
		nodes, edges, _ := ThinkingChain(msgs)
		if len(nodes) != 2 {
			t.Fatalf("期望 2 节点（想+动作配对），得 %d：%+v", len(nodes), nodes)
		}
		if nodes[1].Kind != KindAction || nodes[1].Title != "调用 curl" || nodes[1].Status != "done" {
			t.Errorf("动作节点不符：%+v", nodes[1])
		}
		if len(edges) != 1 || edges[0].From != "m1" || edges[0].To != "m2" || edges[0].Type != EdgeFlow {
			t.Errorf("flow 边不符：%+v", edges)
		}
	})

	t.Run("tool_result 带错误翻 error 态", func(t *testing.T) {
		msgs := []conversation.Message{
			mkEventMsg("m1", evToolCall, map[string]any{"ToolName": "sqlmap"}),
			mkEventMsg("m2", evToolResult, map[string]any{"ToolName": "sqlmap", "Err": "timeout"}),
		}
		nodes, _, _ := ThinkingChain(msgs)
		if len(nodes) != 1 || nodes[0].Status != "error" {
			t.Errorf("期望 1 个 error 动作节点，得 %+v", nodes)
		}
	})

	t.Run("无配对的 tool_result 独立成节点", func(t *testing.T) {
		msgs := []conversation.Message{
			mkEventMsg("m1", evToolResult, map[string]any{"ToolName": "nmap"}),
		}
		nodes, _, _ := ThinkingChain(msgs)
		if len(nodes) != 1 || nodes[0].Title != "结果 nmap" {
			t.Errorf("期望独立结果节点，得 %+v", nodes)
		}
	})

	t.Run("spawn 出 agent 边界节点", func(t *testing.T) {
		msgs := []conversation.Message{
			mkEventMsg("m1", evSpawn, map[string]any{"Args": `{"subagent_type":"reconnaissance"}`}),
		}
		nodes, _, _ := ThinkingChain(msgs)
		if len(nodes) != 1 || nodes[0].Kind != KindAgent || nodes[0].Title != "派发 reconnaissance" {
			t.Errorf("agent 节点不符：%+v", nodes)
		}
	})
}

func TestThinkingChainTree(t *testing.T) {
	// orchestrator 想 → spawn(exploitation) → exploitation 想 → exploitation 做
	msgs := []conversation.Message{
		mkEventMsg("o1", evReasoning, map[string]any{"AgentName": "orchestrator", "Text": "先规划"}),
		mkEventMsg("s1", evSpawn, map[string]any{"AgentName": "orchestrator", "Args": `{"subagent_type":"exploitation"}`}),
		mkEventMsg("e1", evReasoning, map[string]any{"AgentName": "exploitation", "Text": "试上传"}),
		mkEventMsg("e2", evToolCall, map[string]any{"AgentName": "exploitation", "ToolName": "curl"}),
	}
	nodes, edges, _ := ThinkingChain(msgs)
	if len(nodes) != 4 {
		t.Fatalf("节点数=%d 期望 4：%+v", len(nodes), nodes)
	}

	parent := map[string]string{}
	agent := map[string]string{}
	for _, n := range nodes {
		parent[n.ID] = n.ParentID
		agent[n.ID] = n.Agent
	}
	// 主线：o1 为根，spawn s1 挂 o1
	if parent["o1"] != "" {
		t.Errorf("o1 应为根，得 parent=%q", parent["o1"])
	}
	if parent["s1"] != "o1" {
		t.Errorf("spawn s1 应挂 o1，得 %q", parent["s1"])
	}
	// 子代理分支：e1 挂 spawn s1（首节点），e2 顺接 e1
	if parent["e1"] != "s1" {
		t.Errorf("exploitation 首节点 e1 应挂 spawn s1，得 %q", parent["e1"])
	}
	if parent["e2"] != "e1" {
		t.Errorf("e2 应顺接 e1，得 %q", parent["e2"])
	}
	if agent["e1"] != "exploitation" {
		t.Errorf("e1 agent 应 exploitation，得 %q", agent["e1"])
	}
	if len(edges) != 3 { // 有父的节点各一条父子边
		t.Errorf("边数=%d 期望 3：%+v", len(edges), edges)
	}
}

func TestProjectLinksFindingToTrace(t *testing.T) {
	// write_finding 动作 + 返回 {"id":"f1"} → finding f1 应挂到该动作并有 evidence 边
	msgs := []conversation.Message{
		mkEventMsg("a1", evToolCall, map[string]any{"AgentName": "exploitation", "ToolName": "write_finding"}),
		mkEventMsg("a2", evToolResult, map[string]any{"AgentName": "exploitation", "ToolName": "write_finding", "Result": `{"id":"f1"}`}),
	}
	findings := []finding.VulnFinding{mkFinding("f1", "h", "high", "SQLi")}
	g := Project("o", msgs, findings)

	hasEvidence := false
	for _, e := range g.Edges {
		if e.Type == EdgeEvidence && e.From == "a1" && e.To == "f1" {
			hasEvidence = true
		}
	}
	if !hasEvidence {
		t.Errorf("期望 evidence 边 a1→f1，实际边：%+v", g.Edges)
	}
	for _, n := range g.Nodes {
		if n.ID == "f1" && n.ParentID != "a1" {
			t.Errorf("finding f1 应挂到 write_finding 动作 a1，得 parent=%q", n.ParentID)
		}
	}
}

func TestProjectMergesChains(t *testing.T) {
	msgs := []conversation.Message{
		mkEventMsg("m1", evReasoning, map[string]any{"Text": "上传配合穿越"}),
	}
	findings := []finding.VulnFinding{
		mkFinding("a", "h1", "medium", "文件上传"),
		mkFinding("b", "h1", "critical", "组合 RCE", "a"),
	}
	g := Project("owner-1", msgs, findings)

	if g.OwnerID != "owner-1" {
		t.Errorf("OwnerID=%q", g.OwnerID)
	}
	// 1 想节点 + 2 漏洞节点
	if len(g.Nodes) != 3 {
		t.Errorf("节点数=%d，期望 3：%+v", len(g.Nodes), g.Nodes)
	}
	// 思维链 0 flow 边（单节点）+ 成果链 1 depends_on 边
	var dep int
	for _, e := range g.Edges {
		if e.Type == EdgeDependsOn {
			dep++
		}
	}
	if dep != 1 {
		t.Errorf("depends_on 边数=%d，期望 1", dep)
	}
}
