// Package graph 实现 graph_node + graph_edge 持久化层：
// engagement 范围内的端点/资产/角色/会话节点 与 它们之间的关系边；
// 以 (engagement_id, kind, dedup_key) / (engagement_id, from_id, to_id, kind)
// 为 UNIQUE 去重键，重复 upsert 时合并 payload（jsonb || EXCLUDED.payload）。
package graph

import (
	"encoding/json"
	"time"
)

// Node 是 graph_node 表行的 Go 表示。
type Node struct {
	ID           string
	EngagementID string
	Kind         string
	Payload      json.RawMessage
	DedupKey     string
	CreatedAt    time.Time
}

// Edge 是 graph_edge 表行的 Go 表示。
type Edge struct {
	ID           string
	EngagementID string
	FromID       string
	ToID         string
	Kind         string
	Payload      json.RawMessage
	CreatedAt    time.Time
}

// NodeParams 是 Store.UpsertNode 的入参。Payload 为 nil 时落空对象 '{}'。
type NodeParams struct {
	EngagementID string
	Kind         string
	DedupKey     string
	Payload      json.RawMessage
}

// EdgeParams 是 Store.UpsertEdge 的入参。FromID / ToID 必须先以 Node 存在。
type EdgeParams struct {
	EngagementID string
	FromID       string
	ToID         string
	Kind         string
	Payload      json.RawMessage
}
