package attackgraph

import (
	"context"
	"errors"
	"testing"

	"github.com/V3teran/liusha/internal/conversation"
)

// echoSummarizer 返回固定标题（或错误），便于断言 ① 提炼节点的产出。
type echoSummarizer struct {
	title string
	err   error
}

func (e echoSummarizer) Summarize(_ context.Context, _ string) (string, error) {
	if e.err != nil {
		return "", e.err
	}
	return e.title, nil
}

func TestEnrichFillsHypothesisForUnmarkedTask(t *testing.T) {
	// orchestrator 一段推理 + 一个探测，无 agent 自标判断 → ① 提炼补一个 llm 判断，探测改挂其下。
	msgs := []conversation.Message{
		mkEventMsg("m1", evReasoning, map[string]any{"AgentName": "orchestrator", "Text": "看看有没有注入点"}),
		mkEventMsg("t1", evToolCall, map[string]any{"AgentName": "orchestrator", "ToolName": "sqlmap", "CallID": "c1"}),
		mkEventMsg("r1", evToolResult, map[string]any{"AgentName": "orchestrator", "ToolName": "sqlmap", "CallID": "c1"}),
	}
	g := ProjectEnriched(context.Background(), "task-1", msgs, nil, echoSummarizer{title: "验证是否存在 SQL 注入"})

	if !g.Enriched {
		t.Error("跑过 ① 应 Enriched=true")
	}
	by := nodeByID(g.Nodes)
	hypoID := llmHypothesisID(rootTaskID)
	hypo, ok := by[hypoID]
	if !ok {
		t.Fatalf("期望 ① 提炼出 llm 判断节点 %s，实际节点：%+v", hypoID, g.Nodes)
	}
	if hypo.Kind != KindHypothesis || hypo.Provenance != ProvLLM {
		t.Errorf("① 判断节点字段不符：%+v", hypo)
	}
	if hypo.Title != "验证是否存在 SQL 注入" {
		t.Errorf("判断标题应为 LLM 提炼结果，得 %q", hypo.Title)
	}
	if hypo.ParentID != rootTaskID {
		t.Errorf("① 判断应挂根任务，得 parent=%q", hypo.ParentID)
	}
	// 探测改挂判断下：故事线 任务→判断→探测。
	if by["t1"].ParentID != hypoID {
		t.Errorf("探测应改挂 ① 判断下，得 parent=%q", by["t1"].ParentID)
	}
}

func TestEnrichRespectsAgentMark(t *testing.T) {
	// agent 已自标判断（②）→ ① 不再兜底，不产 llm 判断（避免重复/冲突）。
	msgs := []conversation.Message{
		mkEventMsg("h1", evInsight, map[string]any{
			"AgentName": "orchestrator",
			"Args":      insightArgsJSON(insightHypothesis, "疑似上传漏洞", false),
		}),
		mkEventMsg("m1", evReasoning, map[string]any{"AgentName": "orchestrator", "Text": "继续深入"}),
		mkEventMsg("t1", evToolCall, map[string]any{"AgentName": "orchestrator", "ToolName": "curl", "CallID": "c1"}),
		mkEventMsg("r1", evToolResult, map[string]any{"AgentName": "orchestrator", "ToolName": "curl", "CallID": "c1"}),
	}
	g := ProjectEnriched(context.Background(), "task-1", msgs, nil, echoSummarizer{title: "不该出现"})

	for _, n := range g.Nodes {
		if n.Provenance == ProvLLM {
			t.Errorf("②已覆盖的任务线不应再有 ① llm 判断，得 %+v", n)
		}
	}
	// agent 自标判断仍在。
	if by := nodeByID(g.Nodes); by["h1"].Provenance != ProvAgent {
		t.Errorf("agent 自标判断应保留 provenance=agent")
	}
}

func TestEnrichSkipsTaskWithoutReasoning(t *testing.T) {
	// 任务线只有探测、无推理 → 无可提炼素材，① 不产判断（宁缺毋滥）。
	msgs := []conversation.Message{
		mkEventMsg("t1", evToolCall, map[string]any{"AgentName": "orchestrator", "ToolName": "nmap", "CallID": "c1"}),
		mkEventMsg("r1", evToolResult, map[string]any{"AgentName": "orchestrator", "ToolName": "nmap", "CallID": "c1"}),
	}
	g := ProjectEnriched(context.Background(), "task-1", msgs, nil, echoSummarizer{title: "不该出现"})
	for _, n := range g.Nodes {
		if n.Provenance == ProvLLM {
			t.Errorf("无推理任务线不应产 ① 判断，得 %+v", n)
		}
	}
}

func TestEnrichSummarizerFailureDropsNode(t *testing.T) {
	// LLM 提炼失败 → 该线不插空判断（宁缺毋滥），图退化为骨架，探测仍挂任务。
	msgs := []conversation.Message{
		mkEventMsg("m1", evReasoning, map[string]any{"AgentName": "orchestrator", "Text": "找注入"}),
		mkEventMsg("t1", evToolCall, map[string]any{"AgentName": "orchestrator", "ToolName": "sqlmap", "CallID": "c1"}),
		mkEventMsg("r1", evToolResult, map[string]any{"AgentName": "orchestrator", "ToolName": "sqlmap", "CallID": "c1"}),
	}
	g := ProjectEnriched(context.Background(), "task-1", msgs, nil, echoSummarizer{err: errors.New("llm down")})
	by := nodeByID(g.Nodes)
	if _, ok := by[llmHypothesisID(rootTaskID)]; ok {
		t.Error("提炼失败不应插入判断节点")
	}
	if by["t1"].ParentID != rootTaskID {
		t.Errorf("提炼失败时探测应仍挂任务根，得 parent=%q", by["t1"].ParentID)
	}
}

func TestEnrichNilSummarizerEqualsSkeleton(t *testing.T) {
	// s=nil → ProjectEnriched 等价 Project（跳过 ①，Enriched=false）。
	msgs := []conversation.Message{
		mkEventMsg("m1", evReasoning, map[string]any{"AgentName": "orchestrator", "Text": "找注入"}),
		mkEventMsg("t1", evToolCall, map[string]any{"AgentName": "orchestrator", "ToolName": "sqlmap", "CallID": "c1"}),
		mkEventMsg("r1", evToolResult, map[string]any{"AgentName": "orchestrator", "ToolName": "sqlmap", "CallID": "c1"}),
	}
	var noSummarizer Summarizer
	g := ProjectEnriched(context.Background(), "task-1", msgs, nil, noSummarizer)
	if g.Enriched {
		t.Error("s=nil 时不应标记 Enriched")
	}
	for _, n := range g.Nodes {
		if n.Provenance == ProvLLM {
			t.Errorf("s=nil 不应产 ① 判断，得 %+v", n)
		}
	}
}
