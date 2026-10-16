package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/framework/runtime"
)

// 观察归属在写入时精确建立：Engine.createObservationNode 建节点即落 generates 边
// （action → observation），不存在收割期的二次归属查询——并行执行 action 时各自
// 的观察天然只归各自的 action，不会串。Attempt 经 observationToAttempt 携带该
// 节点 ID，repro 域信封整体透传（复现门按 domain 分发）。
func TestObservationOwnership_ByGeneratesEdgeAtWriteTime(t *testing.T) {
	graph := explorationgraph.NewMemoryStore()
	ctx := context.Background()
	const taskID = "t-harvest"

	engine := NewEngine(EngineConfig{Graph: graph, Logger: zerolog.Nop()})

	mkAction := func(id string) {
		st := explorationgraph.StateOpen
		_, _ = graph.CreateNode(ctx, explorationgraph.Node{
			ID: id, TaskID: taskID, Kind: core.KindAction, State: &st,
			Content: json.RawMessage(`{"instruction":"x"}`),
		})
	}
	mkAction("act-A")
	mkAction("act-B")

	envelope := json.RawMessage(`{"domain":"generic","recipe":{"steps":"do"},"assert":{"description":"see x"}}`)

	// 各 action 走 Engine 写入路径：建观察节点 + 落归属边 + 转 Attempt
	nodeA, err := engine.createObservationNode(ctx, taskID, "act-A",
		Observation{Statement: "A 的假设", Repro: envelope})
	if err != nil {
		t.Fatalf("创建 act-A 观察节点失败: %v", err)
	}
	attA, err := engine.observationToAttempt(taskID, nodeA,
		Observation{Statement: "A 的假设", Repro: envelope})
	if err != nil {
		t.Fatalf("转换 act-A Attempt 失败: %v", err)
	}

	nodeB, err := engine.createObservationNode(ctx, taskID, "act-B",
		Observation{Statement: "B 的假设", Repro: envelope})
	if err != nil {
		t.Fatalf("创建 act-B 观察节点失败: %v", err)
	}

	// 归属精确性：act-A 的 generates 边只指向自己的观察，act-B 同理
	edges, err := graph.ListEdgesForAPI(ctx, taskID)
	if err != nil {
		t.Fatalf("查询边失败: %v", err)
	}
	owns := map[string][]string{} // action → 其 generates 指向的观察
	for _, e := range edges {
		if e.Rel == explorationgraph.RelGenerates {
			owns[e.SrcID] = append(owns[e.SrcID], e.DstID)
		}
	}
	if len(owns["act-A"]) != 1 || owns["act-A"][0] != nodeA.ID {
		t.Fatalf("act-A 应只归属自己的观察 %s, got %v", nodeA.ID, owns["act-A"])
	}
	if len(owns["act-B"]) != 1 || owns["act-B"][0] != nodeB.ID {
		t.Fatalf("act-B 应只归属自己的观察 %s, got %v", nodeB.ID, owns["act-B"])
	}

	// Attempt 携带归属节点 ID，kind 为晋升目标 result
	if attA.NodeID != nodeA.ID || attA.Kind != "result" {
		t.Fatalf("Attempt 归属错误: node=%s kind=%s", attA.NodeID, attA.Kind)
	}

	// 信封整体透传（字节级），domain 可供复现门分发
	if !bytes.Equal(bytes.TrimSpace(attA.Primitives), envelope) {
		t.Fatalf("Attempt 的 repro 应为信封整体透传, got %s", attA.Primitives)
	}
	var env struct {
		Domain string `json:"domain"`
	}
	if json.Unmarshal(attA.Primitives, &env) != nil || env.Domain != "generic" {
		t.Fatalf("信封 domain 应为 generic, got %s", attA.Primitives)
	}
}

// 收割循环的配方归一化必须传播到 Attempt——节点与 Attempt 共用同一矫正结果。
// 回归背景：归一化曾藏在节点构造内部（值拷贝上矫正），事件路径的 Attempt
// 携带原始破碎形状（数组信封/标量断言）直通复现门被确定性杀死，4 条真漏洞
// 假设冤死为 invalid_recipe，而节点上存的却是修复后的配方。
func TestProcessExecutorOutput_NormalizationPropagatesToAttempt(t *testing.T) {
	graph := explorationgraph.NewMemoryStore()
	out := &bytes.Buffer{}
	e := NewEngine(EngineConfig{
		Graph:  graph,
		Logger: zerolog.New(out),
	})

	action := explorationgraph.Node{
		ID:      "act-norm",
		TaskID:  "t-norm",
		Kind:    core.KindAction,
		Content: json.RawMessage(`{"type":"testing","instruction":"i"}`),
	}
	// 内存图要求边两侧节点先存在（生产里 action 由 planner 落图）
	if _, err := graph.CreateNode(context.Background(), action); err != nil {
		t.Fatalf("建 action 节点失败: %v", err)
	}

	// 数组形状 repro + 标量断言——两种曾漏杀真假设的原始形状
	rawRepro := json.RawMessage(`["登录获取会话","GET http://t.local/x?name=<script>alert(1)</script> 观察回显"]`)
	result := &runtime.ReActResult{
		FinalAnswer: "```json\n{\"status\":\"completed\",\"summary\":\"s\",\"observations\":[{" +
			"\"statement\":\"xss_r name 参数存在反射型 XSS\"," +
			"\"reasoning\":\"r\",\"test_plan\":\"p\",\"confidence\":\"high\",\"severity\":\"high\"," +
			"\"repro\":" + string(rawRepro) + "}]}\n```",
	}

	attempts, err := e.processExecutorOutput(context.Background(), action, result)
	if err != nil {
		t.Fatalf("processExecutorOutput: %v", err)
	}
	if len(attempts) != 1 {
		t.Fatalf("应产出 1 个 Attempt: %d\nlogs:\n%s", len(attempts), out.String())
	}

	// Attempt.Primitives 必须是归一化后的域信封对象，而非原始数组
	var env struct {
		Domain string `json:"domain"`
	}
	if err := json.Unmarshal(attempts[0].Primitives, &env); err != nil {
		t.Fatalf("Attempt.Primitives 应为归一化信封对象: %v (%s)", err, attempts[0].Primitives)
	}
	if env.Domain != "generic" && env.Domain != "web" {
		t.Fatalf("归一化后应有合法 domain: %q", env.Domain)
	}

	// 节点上存的与 Attempt 携带的是同一份配方（形状一致）
	node, err := graph.GetNode(context.Background(), attempts[0].NodeID)
	if err != nil || node == nil {
		t.Fatalf("观察节点应落图: %v", err)
	}
	var content struct {
		Repro json.RawMessage `json:"repro"`
	}
	if err := json.Unmarshal(node.Content, &content); err != nil || len(content.Repro) == 0 {
		t.Fatalf("节点应携带 repro: %v", err)
	}
	if string(content.Repro) != string(attempts[0].Primitives) {
		t.Fatalf("节点与 Attempt 配方形状不一致:\nnode=%s\nattempt=%s", content.Repro, attempts[0].Primitives)
	}
}
