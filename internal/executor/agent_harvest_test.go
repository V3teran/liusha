package executor

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/bus"
	"github.com/V3teran/liusha/internal/evaluator"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
)

// 收割按 generates 边精确归属（替代时间窗全表扫）——并行执行 action 时
// 时间窗重叠会导致同一假设双收割/双验证/双 finding。
func TestHarvest_ByGeneratesEdgePreciseOwnership(t *testing.T) {
	graph := explorationgraph.NewMemoryStore()
	ctx := context.Background()
	const taskID = "t-harvest"

	mkAction := func(id string) {
		st := explorationgraph.StateOpen
		_, _ = graph.CreateNode(ctx, explorationgraph.Node{
			ID: id, TaskID: taskID, Kind: core.KindAction, State: &st,
			Content: json.RawMessage(`{"instruction":"x"}`),
		})
	}
	mkHypo := func(id, stmt string) {
		_, _ = graph.CreateNode(ctx, explorationgraph.Node{
			ID: id, TaskID: taskID, Kind: core.KindObservation,
			Content: json.RawMessage(`{"statement":"` + stmt + `","repro":{"domain":"generic","recipe":{"steps":"do"},"assert":{"description":"see x"}}}`),
		})
	}
	mkEdge := func(src, dst string) {
		_ = graph.CreateBusinessEdge(ctx, explorationgraph.Edge{
			TaskID: taskID, SrcID: src, Rel: explorationgraph.RelGenerates, DstID: dst,
		})
	}

	mkAction("act-A")
	mkAction("act-B")
	mkHypo("obs-A1", "A 的假设") // A 产出
	mkHypo("obs-B1", "B 的假设") // B 产出
	mkHypo("obs-裸", "无归属假设")  // 无边（如历史存量）——不收割
	mkEdge("act-A", "obs-A1")
	mkEdge("act-B", "obs-B1")

	a := NewAgent(AgentConfig{
		TaskID: taskID, Graph: graph,
		EventBus: bus.New(ctx), Logger: zerolog.Nop(),
	})

	got := a.harvestObservationProposals(ctx, "act-A")
	if len(got) != 1 {
		t.Fatalf("act-A 应只收割自己的 1 个假设（不含 B 的与无归属的）, got %d", len(got))
	}
	if got[0].NodeID != "obs-A1" {
		t.Fatalf("归属错误: got %+v", got[0].NodeID)
	}
	// 信封整体透传
	var env struct {
		Domain string `json:"domain"`
	}
	if json.Unmarshal(got[0].Primitives, &env) != nil || env.Domain != "generic" {
		t.Fatalf("收割产物应为归一化信封, got %s", got[0].Primitives)
	}

	gotB := a.harvestObservationProposals(ctx, "act-B")
	if len(gotB) != 1 || gotB[0].NodeID != "obs-B1" {
		t.Fatalf("act-B 应只收割 obs-B1, got %+v", gotB)
	}

	// 不存在的 action：空收割（无 panic）
	if got := a.harvestObservationProposals(ctx, "act-none"); len(got) != 0 {
		t.Fatalf("无归属 action 应空收割, got %d", len(got))
	}
	_ = evaluator.Attempt{} // 保持 import 引用
}
