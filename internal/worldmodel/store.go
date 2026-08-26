package worldmodel

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store 是世界模型的持久化层（统一 Node 和 Edge）
type Store struct {
	pool *pgxpool.Pool
}

// NewStore 创建 Store
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// ─────────────────────────────────────────────
//  Node 操作
// ─────────────────────────────────────────────

// CreateNode 创建节点
func (s *Store) CreateNode(ctx context.Context, node Node) (string, error) {
	query := `
		INSERT INTO wm_node (
			id, task_id, kind, content,
			state, complexity, depends_on, blocked_reason,
			confidence,
			priority, owner, source_type, source_id, tags,
			created_at, updated_at, completed_at
		) VALUES (
			$1, $2, $3, $4,
			$5, $6, $7, $8,
			$9,
			$10, $11, $12, $13, $14,
			$15, $16, $17
		) RETURNING id
	`

	var id string
	err := s.pool.QueryRow(ctx, query,
		node.ID, node.TaskID, node.Kind, node.Content,
		node.State, node.Complexity, node.DependsOn, node.BlockedReason,
		node.Confidence,
		node.Priority, node.Owner, node.SourceType, node.SourceID, node.Tags,
		node.CreatedAt, node.UpdatedAt, node.CompletedAt,
	).Scan(&id)

	if err != nil {
		return "", fmt.Errorf("create node: %w", err)
	}
	return id, nil
}

// GetNode 获取单个节点
func (s *Store) GetNode(ctx context.Context, id string) (*Node, error) {
	query := `
		SELECT id, task_id, kind, content,
		       state, complexity, depends_on, blocked_reason,
		       confidence,
		       priority, owner, source_type, source_id, tags,
		       created_at, updated_at, completed_at
		FROM wm_node
		WHERE id = $1
	`

	var node Node
	err := s.pool.QueryRow(ctx, query, id).Scan(
		&node.ID, &node.TaskID, &node.Kind, &node.Content,
		&node.State, &node.Complexity, &node.DependsOn, &node.BlockedReason,
		&node.Confidence,
		&node.Priority, &node.Owner, &node.SourceType, &node.SourceID, &node.Tags,
		&node.CreatedAt, &node.UpdatedAt, &node.CompletedAt,
	)

	if err != nil {
		return nil, fmt.Errorf("get node: %w", err)
	}
	return &node, nil
}

// ListNodesByKind 列出指定 task 和 kind 的节点
func (s *Store) ListNodesByKind(ctx context.Context, taskID string, kind NodeKind) ([]Node, error) {
	query := `
		SELECT id, task_id, kind, content,
		       state, complexity, depends_on, blocked_reason,
		       confidence,
		       priority, owner, source_type, source_id, tags,
		       created_at, updated_at, completed_at
		FROM wm_node
		WHERE task_id = $1 AND kind = $2
		ORDER BY priority DESC, created_at ASC
	`

	rows, err := s.pool.Query(ctx, query, taskID, kind)
	if err != nil {
		return nil, fmt.Errorf("list nodes by kind: %w", err)
	}
	defer rows.Close()

	var nodes []Node
	for rows.Next() {
		var node Node
		err := rows.Scan(
			&node.ID, &node.TaskID, &node.Kind, &node.Content,
			&node.State, &node.Complexity, &node.DependsOn, &node.BlockedReason,
			&node.Confidence,
			&node.Priority, &node.Owner, &node.SourceType, &node.SourceID, &node.Tags,
			&node.CreatedAt, &node.UpdatedAt, &node.CompletedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan node: %w", err)
		}
		nodes = append(nodes, node)
	}

	return nodes, rows.Err()
}

// ListMovesByState 列出指定 task 和 state 的 Move 节点
func (s *Store) ListMovesByState(ctx context.Context, taskID string, state State) ([]Node, error) {
	query := `
		SELECT id, task_id, kind, content,
		       state, complexity, depends_on, blocked_reason,
		       confidence,
		       priority, owner, source_type, source_id, tags,
		       created_at, updated_at, completed_at
		FROM wm_node
		WHERE task_id = $1 AND kind = 'move' AND state = $2
		ORDER BY priority DESC, created_at ASC
	`

	rows, err := s.pool.Query(ctx, query, taskID, state)
	if err != nil {
		return nil, fmt.Errorf("list moves by state: %w", err)
	}
	defer rows.Close()

	var nodes []Node
	for rows.Next() {
		var node Node
		err := rows.Scan(
			&node.ID, &node.TaskID, &node.Kind, &node.Content,
			&node.State, &node.Complexity, &node.DependsOn, &node.BlockedReason,
			&node.Confidence,
			&node.Priority, &node.Owner, &node.SourceType, &node.SourceID, &node.Tags,
			&node.CreatedAt, &node.UpdatedAt, &node.CompletedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan node: %w", err)
		}
		nodes = append(nodes, node)
	}

	return nodes, rows.Err()
}

// UpdateMoveState 更新 Move 的状态
func (s *Store) UpdateMoveState(ctx context.Context, id string, state State, blockedReason *string) error {
	query := `
		UPDATE wm_node
		SET state = $2, blocked_reason = $3, updated_at = now(),
		    completed_at = CASE WHEN $2 IN ('done', 'failed', 'exhausted', 'aborted') THEN now() ELSE NULL END
		WHERE id = $1 AND kind = 'move'
	`

	tag, err := s.pool.Exec(ctx, query, id, state, blockedReason)
	if err != nil {
		return fmt.Errorf("update move state: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("move not found: %s", id)
	}
	return nil
}

// UpdateNodeConfidence 更新 observation/discovery 的置信度
func (s *Store) UpdateNodeConfidence(ctx context.Context, id string, confidence Confidence) error {
	query := `
		UPDATE wm_node
		SET confidence = $2, updated_at = now()
		WHERE id = $1 AND kind IN ('observation', 'discovery')
	`

	tag, err := s.pool.Exec(ctx, query, id, confidence)
	if err != nil {
		return fmt.Errorf("update node confidence: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("node not found or not observation/discovery: %s", id)
	}
	return nil
}

// DeleteNode 删除节点（级联删除关联边）
func (s *Store) DeleteNode(ctx context.Context, id string) error {
	query := `DELETE FROM wm_node WHERE id = $1`
	_, err := s.pool.Exec(ctx, query, id)
	if err != nil {
		return fmt.Errorf("delete node: %w", err)
	}
	return nil
}

// ─────────────────────────────────────────────
//  Edge 操作
// ─────────────────────────────────────────────

// CreateEdge 创建边（幂等）
func (s *Store) CreateEdge(ctx context.Context, edge Edge) error {
	query := `
		INSERT INTO wm_edge (task_id, src_id, rel, dst_id, attrs, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (task_id, src_id, rel, dst_id) DO NOTHING
	`

	_, err := s.pool.Exec(ctx, query,
		edge.TaskID, edge.SrcID, edge.Rel, edge.DstID, edge.Attrs, edge.CreatedAt,
	)

	if err != nil {
		return fmt.Errorf("create edge: %w", err)
	}
	return nil
}

// ListEdgesFrom 列出从指定节点出发的边
func (s *Store) ListEdgesFrom(ctx context.Context, taskID, srcID string) ([]Edge, error) {
	query := `
		SELECT task_id, src_id, rel, dst_id, attrs, created_at
		FROM wm_edge
		WHERE task_id = $1 AND src_id = $2
		ORDER BY created_at ASC
	`

	rows, err := s.pool.Query(ctx, query, taskID, srcID)
	if err != nil {
		return nil, fmt.Errorf("list edges from: %w", err)
	}
	defer rows.Close()

	var edges []Edge
	for rows.Next() {
		var edge Edge
		err := rows.Scan(&edge.TaskID, &edge.SrcID, &edge.Rel, &edge.DstID, &edge.Attrs, &edge.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("scan edge: %w", err)
		}
		edges = append(edges, edge)
	}

	return edges, rows.Err()
}

// ListEdgesTo 列出指向指定节点的边（反向查询）
func (s *Store) ListEdgesTo(ctx context.Context, taskID, dstID string) ([]Edge, error) {
	query := `
		SELECT task_id, src_id, rel, dst_id, attrs, created_at
		FROM wm_edge
		WHERE task_id = $1 AND dst_id = $2
		ORDER BY created_at ASC
	`

	rows, err := s.pool.Query(ctx, query, taskID, dstID)
	if err != nil {
		return nil, fmt.Errorf("list edges to: %w", err)
	}
	defer rows.Close()

	var edges []Edge
	for rows.Next() {
		var edge Edge
		err := rows.Scan(&edge.TaskID, &edge.SrcID, &edge.Rel, &edge.DstID, &edge.Attrs, &edge.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("scan edge: %w", err)
		}
		edges = append(edges, edge)
	}

	return edges, rows.Err()
}

// ─────────────────────────────────────────────
//  Verification 操作
// ─────────────────────────────────────────────

// RecordVerification 记录验证结果
func (s *Store) RecordVerification(ctx context.Context, v Verification) (string, error) {
	query := `
		INSERT INTO wm_verification (id, task_id, node_id, primitives, outcome, evidence, duration_ms, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id
	`

	var id string
	err := s.pool.QueryRow(ctx, query,
		v.ID, v.TaskID, v.NodeID, v.Primitives, v.Outcome, v.Evidence, v.DurationMs, v.CreatedAt,
	).Scan(&id)

	if err != nil {
		return "", fmt.Errorf("record verification: %w", err)
	}
	return id, nil
}

// GetVerification 获取验证记录
func (s *Store) GetVerification(ctx context.Context, id string) (*Verification, error) {
	query := `
		SELECT id, task_id, node_id, primitives, outcome, evidence, duration_ms, created_at
		FROM wm_verification
		WHERE id = $1
	`

	var v Verification
	err := s.pool.QueryRow(ctx, query, id).Scan(
		&v.ID, &v.TaskID, &v.NodeID, &v.Primitives, &v.Outcome, &v.Evidence, &v.DurationMs, &v.CreatedAt,
	)

	if err != nil {
		return nil, fmt.Errorf("get verification: %w", err)
	}
	return &v, nil
}

// ─────────────────────────────────────────────
//  便捷方法
// ─────────────────────────────────────────────

// ListOpenMoves 列出待执行的 Move（state=open）
func (s *Store) ListOpenMoves(ctx context.Context, taskID string) ([]Node, error) {
	return s.ListMovesByState(ctx, taskID, StateOpen)
}

// ListCompletedMoves 列出已完成的 Move（state=done）
func (s *Store) ListCompletedMoves(ctx context.Context, taskID string) ([]Node, error) {
	return s.ListMovesByState(ctx, taskID, StateDone)
}

// ListDiscoveries 列出所有重要发现
func (s *Store) ListDiscoveries(ctx context.Context, taskID string) ([]Node, error) {
	return s.ListNodesByKind(ctx, taskID, KindDiscovery)
}

// ListVerifiedDiscoveries 列出已验证的发现
func (s *Store) ListVerifiedDiscoveries(ctx context.Context, taskID string) ([]Node, error) {
	query := `
		SELECT id, task_id, kind, content,
		       state, complexity, depends_on, blocked_reason,
		       confidence,
		       priority, owner, source_type, source_id, tags,
		       created_at, updated_at, completed_at
		FROM wm_node
		WHERE task_id = $1 AND kind = 'discovery' AND confidence = 'verified'
		ORDER BY priority DESC, created_at ASC
	`

	rows, err := s.pool.Query(ctx, query, taskID)
	if err != nil {
		return nil, fmt.Errorf("list verified discoveries: %w", err)
	}
	defer rows.Close()

	var nodes []Node
	for rows.Next() {
		var node Node
		err := rows.Scan(
			&node.ID, &node.TaskID, &node.Kind, &node.Content,
			&node.State, &node.Complexity, &node.DependsOn, &node.BlockedReason,
			&node.Confidence,
			&node.Priority, &node.Owner, &node.SourceType, &node.SourceID, &node.Tags,
			&node.CreatedAt, &node.UpdatedAt, &node.CompletedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan node: %w", err)
		}
		nodes = append(nodes, node)
	}

	return nodes, rows.Err()
}
