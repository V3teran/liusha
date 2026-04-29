package graph

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store 封装 graph_node + graph_edge 表的所有持久化操作。
type Store struct{ pool *pgxpool.Pool }

// NewStore 用 pgxpool 构造 Store。
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// nodeCols 是 graph_node SELECT 路径的统一列序，与 scanNode() 字段一一对应。
const nodeCols = "id, engagement_id, kind, payload, dedup_key, created_at"

// edgeCols 是 graph_edge SELECT 路径的统一列序，与 scanEdge() 字段一一对应。
const edgeCols = "id, engagement_id, from_id, to_id, kind, payload, created_at"

// UpsertNode 按 (engagement_id, kind, dedup_key) UNIQUE 幂等写入 node。
// 冲突时 payload = 旧 jsonb || 新 EXCLUDED.payload（顶层键合并，新值覆盖）。
func (s *Store) UpsertNode(ctx context.Context, p NodeParams) (Node, error) {
	if p.Payload == nil {
		p.Payload = json.RawMessage("{}")
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO graph_node (engagement_id, kind, payload, dedup_key)
		VALUES ($1,$2,$3,$4)
		ON CONFLICT (engagement_id, kind, dedup_key) DO UPDATE
		  SET payload = graph_node.payload || EXCLUDED.payload
		RETURNING `+nodeCols,
		p.EngagementID, p.Kind, []byte(p.Payload), p.DedupKey)
	var n Node
	if err := scanNode(row, &n); err != nil {
		return Node{}, fmt.Errorf("upsert node: %w", err)
	}
	return n, nil
}

// UpsertEdge 按 (engagement_id, from_id, to_id, kind) UNIQUE 幂等写入 edge。
// from_id/to_id 必须先以 graph_node 存在（FK ON DELETE CASCADE）。
func (s *Store) UpsertEdge(ctx context.Context, p EdgeParams) (Edge, error) {
	if p.Payload == nil {
		p.Payload = json.RawMessage("{}")
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO graph_edge (engagement_id, from_id, to_id, kind, payload)
		VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (engagement_id, from_id, to_id, kind) DO UPDATE
		  SET payload = graph_edge.payload || EXCLUDED.payload
		RETURNING `+edgeCols,
		p.EngagementID, p.FromID, p.ToID, p.Kind, []byte(p.Payload))
	var e Edge
	if err := scanEdge(row, &e); err != nil {
		return Edge{}, fmt.Errorf("upsert edge: %w", err)
	}
	return e, nil
}

// GetNode 按主键读取 node。
func (s *Store) GetNode(ctx context.Context, id string) (Node, error) {
	row := s.pool.QueryRow(ctx,
		"SELECT "+nodeCols+" FROM graph_node WHERE id=$1", id)
	var n Node
	if err := scanNode(row, &n); err != nil {
		return Node{}, fmt.Errorf("get node %s: %w", id, err)
	}
	return n, nil
}

// ListNodesByEngagement 按 created_at 升序列出 engagement 的 node，最多 limit 条。
func (s *Store) ListNodesByEngagement(ctx context.Context, engagementID string, limit int) ([]Node, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+nodeCols+`
		FROM graph_node
		WHERE engagement_id=$1
		ORDER BY created_at ASC
		LIMIT $2`, engagementID, limit)
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	defer rows.Close()

	var out []Node
	for rows.Next() {
		var n Node
		if err := scanNode(rows, &n); err != nil {
			return nil, fmt.Errorf("scan node: %w", err)
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate nodes: %w", err)
	}
	return out, nil
}

// ListEdgesByEngagement 按 created_at 升序列出 engagement 的 edge，最多 limit 条。
func (s *Store) ListEdgesByEngagement(ctx context.Context, engagementID string, limit int) ([]Edge, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+edgeCols+`
		FROM graph_edge
		WHERE engagement_id=$1
		ORDER BY created_at ASC
		LIMIT $2`, engagementID, limit)
	if err != nil {
		return nil, fmt.Errorf("list edges: %w", err)
	}
	defer rows.Close()

	var out []Edge
	for rows.Next() {
		var e Edge
		if err := scanEdge(rows, &e); err != nil {
			return nil, fmt.Errorf("scan edge: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate edges: %w", err)
	}
	return out, nil
}

// scanner 抽象 pgx.Row / pgx.Rows 的 Scan 方法。
type scanner interface {
	Scan(dest ...any) error
}

// scanNode 是 nodeCols 列序的统一反序列化点。
func scanNode(r scanner, n *Node) error {
	var payload []byte
	if err := r.Scan(&n.ID, &n.EngagementID, &n.Kind, &payload, &n.DedupKey, &n.CreatedAt); err != nil {
		return err
	}
	n.Payload = payload
	return nil
}

// scanEdge 是 edgeCols 列序的统一反序列化点。
func scanEdge(r scanner, e *Edge) error {
	var payload []byte
	if err := r.Scan(&e.ID, &e.EngagementID, &e.FromID, &e.ToID, &e.Kind, &payload, &e.CreatedAt); err != nil {
		return err
	}
	e.Payload = payload
	return nil
}
