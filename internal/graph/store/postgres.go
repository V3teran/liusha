// internal/graph/store/postgres.go
package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	graphmodel "github.com/V3teran/liusha/internal/graph/model"
)

type Postgres struct {
	pool *pgxpool.Pool
}

func NewPostgres(pool *pgxpool.Pool) *Postgres {
	return &Postgres{pool: pool}
}

func (s *Postgres) AddNode(ctx context.Context, n graphmodel.Node) error {
	payload, err := json.Marshal(n.Payload)
	if err != nil {
		return fmt.Errorf("序列化 payload 失败: %w", err)
	}
	var dedupKey *string
	if n.DedupKey != "" {
		dedupKey = &n.DedupKey
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO graph_node (id, engagement_id, kind, payload, dedup_key)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (engagement_id, dedup_key) WHERE dedup_key IS NOT NULL DO NOTHING`,
		n.ID, n.EngagementID, string(n.Kind), payload, dedupKey,
	)
	if err != nil {
		return fmt.Errorf("插入 graph_node 失败: %w", err)
	}
	return nil
}

func (s *Postgres) AddEdge(ctx context.Context, e graphmodel.Edge) error {
	// 按语义四元组去重：同一 engagement 内相同 src/dst/kind 只保留一条边。
	_, err := s.pool.Exec(ctx, `
		INSERT INTO graph_edge (id, engagement_id, src_id, dst_id, kind)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (engagement_id, src_id, dst_id, kind) DO NOTHING`,
		e.ID, e.EngagementID, e.SrcID, e.DstID, string(e.Kind),
	)
	if err != nil {
		return fmt.Errorf("插入 graph_edge 失败: %w", err)
	}
	return nil
}

func (s *Postgres) GetNode(ctx context.Context, id uuid.UUID) (graphmodel.Node, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, engagement_id, kind, payload, COALESCE(dedup_key,''), created_at
		FROM graph_node WHERE id = $1`, id)
	return scanNode(row)
}

func (s *Postgres) ListNodes(ctx context.Context, engID uuid.UUID, kind graphmodel.NodeKind) ([]graphmodel.Node, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, engagement_id, kind, payload, COALESCE(dedup_key,''), created_at
		FROM graph_node
		WHERE engagement_id = $1 AND kind = $2
		ORDER BY created_at DESC`, engID, string(kind))
	if err != nil {
		return nil, fmt.Errorf("查询 graph_node 失败: %w", err)
	}
	defer rows.Close()
	var nodes []graphmodel.Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, n)
	}
	return nodes, rows.Err()
}

func (s *Postgres) ListEdges(ctx context.Context, engID uuid.UUID, nodeID uuid.UUID) ([]graphmodel.Edge, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, engagement_id, src_id, dst_id, kind, created_at
		FROM graph_edge
		WHERE engagement_id = $1 AND (src_id = $2 OR dst_id = $2)
		ORDER BY created_at DESC`, engID, nodeID)
	if err != nil {
		return nil, fmt.Errorf("查询 graph_edge 失败: %w", err)
	}
	defer rows.Close()
	var edges []graphmodel.Edge
	for rows.Next() {
		var e graphmodel.Edge
		if err := rows.Scan(&e.ID, &e.EngagementID, &e.SrcID, &e.DstID, &e.Kind, &e.CreatedAt); err != nil {
			return nil, err
		}
		edges = append(edges, e)
	}
	return edges, rows.Err()
}

func scanNode(row pgx.Row) (graphmodel.Node, error) {
	var n graphmodel.Node
	var rawPayload []byte
	var kind string
	if err := row.Scan(&n.ID, &n.EngagementID, &kind, &rawPayload, &n.DedupKey, &n.CreatedAt); err != nil {
		return graphmodel.Node{}, fmt.Errorf("扫描 graph_node 失败: %w", err)
	}
	n.Kind = graphmodel.NodeKind(kind)
	if err := json.Unmarshal(rawPayload, &n.Payload); err != nil {
		return graphmodel.Node{}, fmt.Errorf("反序列化 payload 失败: %w", err)
	}
	return n, nil
}
