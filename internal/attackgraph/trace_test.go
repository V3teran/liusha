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

// insightArgsJSON 造 mark_insight 的 Args（{type,text,dead_end}）。
func insightArgsJSON(typ, text string, deadEnd bool) string {
	b, _ := json.Marshal(map[string]any{"type": typ, "text": text, "dead_end": deadEnd})
	return string(b)
}

// nodeByID 把节点按 id 建索引，便于断言。
func nodeByID(nodes []Node) map[string]Node {
	m := make(map[string]Node, len(nodes))
	for _, n := range nodes {
		m[n.ID] = n
	}
	return m
}

func TestSkeletonRootTask(t *testing.T) {
	t.Run("空消息不出任何节点（不伪造主线）", func(t *testing.T) {
		b := skeleton(nil)
		if len(b.nodes) != 0 {
			t.Fatalf("无事件应零节点（根任务惰性合成），得 %d：%+v", len(b.nodes), b.nodes)
		}
	})

	t.Run("非事件消息被忽略，不触发根合成", func(t *testing.T) {
		msgs := []conversation.Message{
			{ID: "u1", Kind: conversation.KindMessage, Content: "用户问句"},
		}
		b := skeleton(msgs)
		if len(b.nodes) != 0 {
			t.Fatalf("非事件消息不应成节点、不应合成根，得 %d：%+v", len(b.nodes), b.nodes)
		}
	})

	t.Run("首个事件到达才合成根任务", func(t *testing.T) {
		msgs := []conversation.Message{
			mkEventMsg("t1", evToolCall, map[string]any{"AgentName": "orchestrator", "ToolName": "nmap", "CallID": "c1"}),
		}
		b := skeleton(msgs)
		by := nodeByID(b.nodes)
		root, ok := by[rootTaskID]
		if !ok || root.Kind != KindTask || root.Provenance != ProvDerived {
			t.Errorf("首个事件应合成根任务，得 %+v", root)
		}
	})
}

func TestSkeletonProbeAggregation(t *testing.T) {
	t.Run("连续工具调用塌缩成一个探测挂根任务下", func(t *testing.T) {
		msgs := []conversation.Message{
			mkEventMsg("t1", evToolCall, map[string]any{"AgentName": "orchestrator", "ToolName": "nmap", "CallID": "c1"}),
			mkEventMsg("r1", evToolResult, map[string]any{"AgentName": "orchestrator", "ToolName": "nmap", "CallID": "c1", "DurationMs": 120}),
			mkEventMsg("t2", evToolCall, map[string]any{"AgentName": "orchestrator", "ToolName": "curl", "CallID": "c2"}),
			mkEventMsg("r2", evToolResult, map[string]any{"AgentName": "orchestrator", "ToolName": "curl", "CallID": "c2", "DurationMs": 80}),
		}
		b := skeleton(msgs)
		probes := 0
		var probe Node
		for _, n := range b.nodes {
			if n.Kind == KindProbe {
				probes++
				probe = n
			}
		}
		if probes != 1 {
			t.Fatalf("期望 1 个聚合探测，得 %d：%+v", probes, b.nodes)
		}
		if probe.ParentID != rootTaskID {
			t.Errorf("探测应挂根任务，得 parent=%q", probe.ParentID)
		}
		if probe.Status != StatusDone {
			t.Errorf("两步均成功，探测应 done，得 %q", probe.Status)
		}
		if probe.DurationMs != 200 {
			t.Errorf("探测耗时应累加=200，得 %d", probe.DurationMs)
		}
	})

	t.Run("reasoning 分隔出两个探测", func(t *testing.T) {
		msgs := []conversation.Message{
			mkEventMsg("t1", evToolCall, map[string]any{"AgentName": "orchestrator", "ToolName": "nmap", "CallID": "c1"}),
			mkEventMsg("r1", evToolResult, map[string]any{"AgentName": "orchestrator", "ToolName": "nmap", "CallID": "c1"}),
			mkEventMsg("think", evReasoning, map[string]any{"AgentName": "orchestrator", "Text": "换个思路"}),
			mkEventMsg("t2", evToolCall, map[string]any{"AgentName": "orchestrator", "ToolName": "curl", "CallID": "c2"}),
			mkEventMsg("r2", evToolResult, map[string]any{"AgentName": "orchestrator", "ToolName": "curl", "CallID": "c2"}),
		}
		b := skeleton(msgs)
		probes := 0
		for _, n := range b.nodes {
			if n.Kind == KindProbe {
				probes++
			}
		}
		if probes != 2 {
			t.Fatalf("reasoning 作边界应分出 2 个探测，得 %d：%+v", probes, b.nodes)
		}
	})

	t.Run("任一调用报错探测翻 failed", func(t *testing.T) {
		msgs := []conversation.Message{
			mkEventMsg("t1", evToolCall, map[string]any{"AgentName": "orchestrator", "ToolName": "sqlmap", "CallID": "c1"}),
			mkEventMsg("r1", evToolResult, map[string]any{"AgentName": "orchestrator", "ToolName": "sqlmap", "CallID": "c1", "Err": "timeout"}),
		}
		b := skeleton(msgs)
		for _, n := range b.nodes {
			if n.Kind == KindProbe && n.Status != StatusFailed {
				t.Errorf("报错探测应 failed，得 %q", n.Status)
			}
		}
	})
}

func TestSkeletonCallIDPairing(t *testing.T) {
	// 两个并发 run_command：c-A 先起后完，c-B 后起先完（交错）。
	// 两者同 agent 且中间无边界事件，聚合进「同一个探测」——
	// 探测终态取决于任一调用是否报错（c-B 失败 → 整个探测 failed）。
	msgs := []conversation.Message{
		mkEventMsg("a1", evToolCall, map[string]any{"AgentName": "orchestrator", "ToolName": "run_command", "CallID": "c-A"}),
		mkEventMsg("b1", evToolCall, map[string]any{"AgentName": "orchestrator", "ToolName": "run_command", "CallID": "c-B"}),
		mkEventMsg("b2", evToolResult, map[string]any{"AgentName": "orchestrator", "ToolName": "run_command", "CallID": "c-B", "Err": "failed"}),
		mkEventMsg("a2", evToolResult, map[string]any{"AgentName": "orchestrator", "ToolName": "run_command", "CallID": "c-A"}),
	}
	b := skeleton(msgs)
	var probe *Node
	for i := range b.nodes {
		if b.nodes[i].Kind == KindProbe {
			if probe != nil {
				t.Fatalf("同 agent 连续调用应聚合为 1 个探测，得多个：%+v", b.nodes)
			}
			probe = &b.nodes[i]
		}
	}
	if probe == nil {
		t.Fatal("未生成探测节点")
	}
	// 两个 CallID 各自回填：openCalls 归零（都收到 result），c-B 报错 → failed。
	if probe.Status != StatusFailed {
		t.Errorf("c-B 失败应使探测 failed，得 %q", probe.Status)
	}
}

func TestSkeletonInsightHypothesis(t *testing.T) {
	// mark_insight(hypothesis) → 判断节点挂当前任务（pursues），并成为后续探测挂载点。
	msgs := []conversation.Message{
		mkEventMsg("h1", evInsight, map[string]any{
			"AgentName": "orchestrator",
			"Args":      insightArgsJSON(insightHypothesis, "疑似存在文件上传漏洞", false),
		}),
		mkEventMsg("t1", evToolCall, map[string]any{"AgentName": "orchestrator", "ToolName": "curl", "CallID": "c1"}),
		mkEventMsg("r1", evToolResult, map[string]any{"AgentName": "orchestrator", "ToolName": "curl", "CallID": "c1"}),
	}
	b := skeleton(msgs)
	by := nodeByID(b.nodes)
	hypo, ok := by["h1"]
	if !ok || hypo.Kind != KindHypothesis {
		t.Fatalf("期望 h1 为判断节点，得 %+v", hypo)
	}
	if hypo.ParentID != rootTaskID {
		t.Errorf("判断应挂根任务（pursues），得 parent=%q", hypo.ParentID)
	}
	if hypo.Provenance != ProvAgent {
		t.Errorf("agent 自标判断应 provenance=agent，得 %q", hypo.Provenance)
	}
	if hypo.Status != StatusOpen {
		t.Errorf("新判断应 open，得 %q", hypo.Status)
	}
	// 后续探测应挂到判断下，而非任务根。
	if by["t1"].ParentID != "h1" {
		t.Errorf("判断后的探测应挂判断下，得 parent=%q", by["t1"].ParentID)
	}
	// ② 已覆盖该任务线：taskHasHypo 置位（① 不再兜底）。
	if !b.taskHasHypo[rootTaskID] {
		t.Error("agent 自标判断应置 taskHasHypo[root]=true")
	}
}

func TestSkeletonInsightSignalAndLoop(t *testing.T) {
	// 判断 → 探测 → 信号（reveals，挂探测下）→ 下一个判断（informs，挂信号下，调查回环）。
	msgs := []conversation.Message{
		mkEventMsg("h1", evInsight, map[string]any{
			"AgentName": "orchestrator",
			"Args":      insightArgsJSON(insightHypothesis, "疑似上传漏洞", false),
		}),
		mkEventMsg("t1", evToolCall, map[string]any{"AgentName": "orchestrator", "ToolName": "curl", "CallID": "c1"}),
		mkEventMsg("r1", evToolResult, map[string]any{"AgentName": "orchestrator", "ToolName": "curl", "CallID": "c1"}),
		mkEventMsg("s1", evInsight, map[string]any{
			"AgentName": "orchestrator",
			"Args":      insightArgsJSON(insightSignal, "上传接口无类型校验", false),
		}),
		mkEventMsg("h2", evInsight, map[string]any{
			"AgentName": "orchestrator",
			"Args":      insightArgsJSON(insightHypothesis, "可上传 webshell 拿 RCE", false),
		}),
	}
	b := skeleton(msgs)
	by := nodeByID(b.nodes)

	if by["s1"].Kind != KindSignal {
		t.Fatalf("s1 应为信号节点，得 %+v", by["s1"])
	}
	// 信号经 reveals 挂最近探测（t1）下。
	if by["s1"].ParentID != "t1" {
		t.Errorf("信号应挂最近探测 t1（reveals），得 parent=%q", by["s1"].ParentID)
	}
	// 下一个判断经 informs 挂信号下（调查回环）。
	if by["h2"].ParentID != "s1" {
		t.Errorf("信号引出的判断应挂信号下（informs），得 parent=%q", by["h2"].ParentID)
	}

	// 边类型校验：t1→s1 reveals，s1→h2 informs。
	et := map[[2]string]string{}
	for _, e := range b.edges() {
		et[[2]string{e.From, e.To}] = e.Type
	}
	if et[[2]string{"t1", "s1"}] != EdgeReveals {
		t.Errorf("t1→s1 应为 reveals，得 %q", et[[2]string{"t1", "s1"}])
	}
	if et[[2]string{"s1", "h2"}] != EdgeInforms {
		t.Errorf("s1→h2 应为 informs，得 %q", et[[2]string{"s1", "h2"}])
	}
}

func TestSkeletonSignalDeadEnd(t *testing.T) {
	msgs := []conversation.Message{
		mkEventMsg("s1", evInsight, map[string]any{
			"AgentName": "orchestrator",
			"Args":      insightArgsJSON(insightSignal, "该端口无有效服务，此路不通", true),
		}),
	}
	b := skeleton(msgs)
	by := nodeByID(b.nodes)
	if by["s1"].Status != StatusRefuted {
		t.Errorf("dead_end 信号应 refuted，得 %q", by["s1"].Status)
	}
}

func TestSkeletonSpawnSubtask(t *testing.T) {
	// orchestrator spawn(exploitation) → 子任务节点挂根任务；子代理事件挂到子任务下。
	msgs := []conversation.Message{
		mkEventMsg("sp1", evSpawn, map[string]any{
			"AgentName": "orchestrator",
			"Args":      `{"subagent_type":"exploitation"}`,
		}),
		mkEventMsg("t1", evToolCall, map[string]any{"AgentName": "exploitation", "ToolName": "curl", "CallID": "c1"}),
		mkEventMsg("r1", evToolResult, map[string]any{"AgentName": "exploitation", "ToolName": "curl", "CallID": "c1"}),
	}
	b := skeleton(msgs)
	by := nodeByID(b.nodes)
	sub, ok := by["sp1"]
	if !ok || sub.Kind != KindTask {
		t.Fatalf("spawn 应生成子任务节点，得 %+v", sub)
	}
	if sub.ParentID != rootTaskID {
		t.Errorf("子任务应挂根任务（spawns），得 parent=%q", sub.ParentID)
	}
	if sub.Agent != "exploitation" {
		t.Errorf("子任务 agent 应为 exploitation，得 %q", sub.Agent)
	}
	// 子代理探测挂子任务下（游标重置到 sp1）。
	if by["t1"].ParentID != "sp1" {
		t.Errorf("子代理探测应挂子任务 sp1，得 parent=%q", by["t1"].ParentID)
	}
	// spawns 边校验。
	found := false
	for _, e := range b.edges() {
		if e.From == rootTaskID && e.To == "sp1" && e.Type == EdgeSpawns {
			found = true
		}
	}
	if !found {
		t.Errorf("期望 root→sp1 spawns 边，实际：%+v", b.edges())
	}
}

func TestSkeletonPrimaryAgentResolution(t *testing.T) {
	// 子代理事件先落库、orchestrator 后到（并发落库常见）：
	// orchestrator 是约定主线，其事件仍挂根任务，不被误判为子链。
	msgs := []conversation.Message{
		mkEventMsg("e1", evReasoning, map[string]any{"AgentName": "exploitation", "Text": "先试上传"}),
		mkEventMsg("o1", evToolCall, map[string]any{"AgentName": "orchestrator", "ToolName": "nmap", "CallID": "c1"}),
		mkEventMsg("or1", evToolResult, map[string]any{"AgentName": "orchestrator", "ToolName": "nmap", "CallID": "c1"}),
	}
	b := skeleton(msgs)
	by := nodeByID(b.nodes)
	// orchestrator 的探测挂根任务（主线）。
	if by["o1"].ParentID != rootTaskID {
		t.Errorf("orchestrator 探测应挂根任务（主线），得 parent=%q", by["o1"].ParentID)
	}
	// exploitation 尚未 spawn 登记 → 回落根任务（事件不丢）。
	if got := b.reasonByTask[rootTaskID]; len(got) != 1 || got[0] != "先试上传" {
		t.Errorf("未登记子代理的 reasoning 应回落根任务收集，得 %+v", got)
	}
}

func TestSkeletonReasoningCollectedNotNode(t *testing.T) {
	// 未被 agent 自标的 reasoning 不成节点，只攒进 reasonByTask 供 ① LLM 兜底。
	msgs := []conversation.Message{
		mkEventMsg("m1", evReasoning, map[string]any{"AgentName": "orchestrator", "Text": "先扫目录"}),
		mkEventMsg("m2", evReasoning, map[string]any{"AgentName": "orchestrator", "Text": "再看响应头"}),
	}
	b := skeleton(msgs)
	if len(b.nodes) != 1 { // 仅根任务
		t.Fatalf("未自标 reasoning 不应成节点，得 %d：%+v", len(b.nodes), b.nodes)
	}
	got := b.reasonByTask[rootTaskID]
	if len(got) != 2 || got[0] != "先扫目录" || got[1] != "再看响应头" {
		t.Errorf("reasoning 应按任务线收集，得 %+v", got)
	}
}

func TestProjectLinksFindingToProbe(t *testing.T) {
	// write_finding 探测 + 返回 {"id":"f1"} → finding f1 挂到该探测下并有 confirms 边。
	msgs := []conversation.Message{
		mkEventMsg("t1", evToolCall, map[string]any{"AgentName": "exploitation", "ToolName": toolWriteFinding, "CallID": "c1"}),
		mkEventMsg("r1", evToolResult, map[string]any{"AgentName": "exploitation", "ToolName": toolWriteFinding, "CallID": "c1", "Result": `{"id":"f1"}`}),
	}
	findings := []finding.VulnFinding{mkFinding("f1", "h", "high", "SQLi")}
	g := Project("task-o", msgs, findings)

	by := nodeByID(g.Nodes)
	if by["f1"].ParentID != "t1" {
		t.Errorf("finding f1 应挂到 write_finding 探测 t1，得 parent=%q", by["f1"].ParentID)
	}
	hasConfirms := false
	for _, e := range g.Edges {
		if e.Type == EdgeConfirms && e.From == "t1" && e.To == "f1" {
			hasConfirms = true
		}
	}
	if !hasConfirms {
		t.Errorf("期望 confirms 边 t1→f1，实际边：%+v", g.Edges)
	}
	// finding 与其探测均在成果路径上。
	if !by["f1"].OnPath || !by["t1"].OnPath {
		t.Errorf("finding 及其探测应 OnPath=true，得 f1=%v t1=%v", by["f1"].OnPath, by["t1"].OnPath)
	}
}

func TestProjectDependsOnChain(t *testing.T) {
	msgs := []conversation.Message{
		mkEventMsg("m1", evReasoning, map[string]any{"AgentName": "orchestrator", "Text": "上传配合穿越"}),
	}
	findings := []finding.VulnFinding{
		mkFinding("a", "h1", "medium", "文件上传"),
		mkFinding("b", "h1", "critical", "组合 RCE", "a"),
	}
	g := Project("task-1", msgs, findings)

	if g.TaskID != "task-1" {
		t.Errorf("TaskID=%q", g.TaskID)
	}
	// 根任务 + 2 漏洞节点（未自标 reasoning 不成节点）。
	if len(g.Nodes) != 3 {
		t.Errorf("节点数=%d，期望 3（根任务+2漏洞）：%+v", len(g.Nodes), g.Nodes)
	}
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
