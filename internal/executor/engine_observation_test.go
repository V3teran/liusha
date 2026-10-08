package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
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
