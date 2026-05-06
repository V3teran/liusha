package common

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/V3teran/liusha/internal/graph"
	"github.com/V3teran/liusha/internal/toolfx"
)

// GraphStore 是 WriteGraph 依赖的最小接口。
//
// 由 *graph.Store 自动满足（internal/graph/store.go:25 UpsertNode + :45 UpsertEdge）。
type GraphStore interface {
	UpsertNode(ctx context.Context, p graph.NodeParams) (graph.Node, error)
	UpsertEdge(ctx context.Context, p graph.EdgeParams) (graph.Edge, error)
}

// WriteGraph — 一次性写一组 node 与/或 edge（去重 upsert）。
//
// 边的 from / to 优先按本次 nodes 列表里出现的 dedup_key 解析为新 ID；解析不到时直接
// 当作已有 node 的 UUID 使用（允许跨 turn 引用历史节点）。
type WriteGraph struct {
	Store        GraphStore
	EngagementID string
}

// Name 返回动作名 "write_graph"。
func (a *WriteGraph) Name() string { return "write_graph" }

// Description 提供给 LLM 的简介。
func (a *WriteGraph) Description() string {
	return "写一组 node 与/或 edge 到 graph（按 dedup_key/(from,to,kind) 幂等 upsert）"
}

// ParametersJSON 给出 nodes / edges 数组 schema。
func (a *WriteGraph) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{
  "type":"object",
  "properties":{
    "nodes":{"type":"array","items":{
      "type":"object",
      "properties":{"kind":{"type":"string"},"dedup_key":{"type":"string"},"payload":{"type":"object"}},
      "required":["kind","dedup_key"]}},
    "edges":{"type":"array","items":{
      "type":"object",
      "properties":{"from":{"type":"string"},"to":{"type":"string"},"kind":{"type":"string"},"payload":{"type":"object"}},
      "required":["from","to","kind"]}}
  }
}`)
}

// Execute 解析参数 → 先 upsert 全部 node 收集 dedup_key→id 映射 → 再 upsert edge。
func (a *WriteGraph) Execute(ctx context.Context, args json.RawMessage) (toolfx.Result, error) {
	var in struct {
		Nodes []struct {
			Kind     string          `json:"kind"`
			DedupKey string          `json:"dedup_key"`
			Payload  json.RawMessage `json:"payload"`
		} `json:"nodes"`
		Edges []struct {
			From    string          `json:"from"`
			To      string          `json:"to"`
			Kind    string          `json:"kind"`
			Payload json.RawMessage `json:"payload"`
		} `json:"edges"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return toolfx.Result{}, fmt.Errorf("解析 write_graph 参数失败: %w", err)
	}

	// dedup_key → 新落库的 node id；edge 引用 from/to 时优先查这里。
	nodeIDs := make(map[string]string, len(in.Nodes))
	for _, n := range in.Nodes {
		if n.Kind == "" || n.DedupKey == "" {
			return toolfx.Result{}, fmt.Errorf("node 必须同时给出 kind 与 dedup_key")
		}
		node, err := a.Store.UpsertNode(ctx, graph.NodeParams{
			EngagementID: a.EngagementID,
			Kind:         n.Kind,
			DedupKey:     n.DedupKey,
			Payload:      n.Payload,
		})
		if err != nil {
			return toolfx.Result{}, fmt.Errorf("upsert node %q: %w", n.DedupKey, err)
		}
		nodeIDs[n.DedupKey] = node.ID
	}

	for _, e := range in.Edges {
		if e.From == "" || e.To == "" || e.Kind == "" {
			return toolfx.Result{}, fmt.Errorf("edge 必须同时给出 from / to / kind")
		}
		// 解析 from / to：先查本次新建的映射，否则当作历史 UUID 直接使用。
		from, ok := nodeIDs[e.From]
		if !ok {
			from = e.From
		}
		to, ok := nodeIDs[e.To]
		if !ok {
			to = e.To
		}
		if _, err := a.Store.UpsertEdge(ctx, graph.EdgeParams{
			EngagementID: a.EngagementID,
			FromID:       from,
			ToID:         to,
			Kind:         e.Kind,
			Payload:      e.Payload,
		}); err != nil {
			return toolfx.Result{}, fmt.Errorf("upsert edge %s->%s: %w", e.From, e.To, err)
		}
	}

	out, _ := json.Marshal(map[string]int{"nodes": len(in.Nodes), "edges": len(in.Edges)})
	return toolfx.Result{Output: out}, nil
}
