package knowledgegraph

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store 是知识图谱的持久化层（统一 Node 和 Edge）
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
	// 自动计算 Action 的 fingerprint
	var fingerprint *string
	if node.Kind == KindAction {
		fp := ComputeActionFingerprint(node.Content)
		fingerprint = &fp
	}

	query := `
		INSERT INTO wm_node (
			id, task_id, kind, content,
			state, complexity, depends_on, blocked_reason, roadmap_step,
			confidence,
			priority, owner, source_type, source_id, tags, metadata,
			fingerprint, version,
			created_at, updated_at, completed_at
		) VALUES (
			$1, $2, $3, $4,
			$5, $6, $7, $8, $9,
			$10,
			$11, $12, $13, $14, $15, $16,
			$17, $18,
			$19, $20, $21
		) RETURNING id
	`

	var id string
	err := s.pool.QueryRow(ctx, query,
		node.ID, node.TaskID, node.Kind, node.Content,
		node.State, node.Complexity, node.DependsOn, node.BlockedReason, node.RoadmapStep,
		node.Confidence,
		node.Priority, node.Owner, node.SourceType, node.SourceID, node.Tags, node.Metadata,
		fingerprint, 1, // fingerprint, version (初始版本为 1)
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
		       priority, owner, source_type, source_id, tags, metadata,
		       created_at, updated_at, completed_at
		FROM wm_node
		WHERE id = $1
	`

	var node Node
	err := s.pool.QueryRow(ctx, query, id).Scan(
		&node.ID, &node.TaskID, &node.Kind, &node.Content,
		&node.State, &node.Complexity, &node.DependsOn, &node.BlockedReason,
		&node.Confidence,
		&node.Priority, &node.Owner, &node.SourceType, &node.SourceID, &node.Tags, &node.Metadata,
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
		       state, complexity, depends_on, blocked_reason, roadmap_step,
		       confidence,
		       priority, owner, source_type, source_id, tags, metadata,
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
			&node.State, &node.Complexity, &node.DependsOn, &node.BlockedReason, &node.RoadmapStep,
			&node.Confidence,
			&node.Priority, &node.Owner, &node.SourceType, &node.SourceID, &node.Tags, &node.Metadata,
			&node.CreatedAt, &node.UpdatedAt, &node.CompletedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan node: %w", err)
		}
		nodes = append(nodes, node)
	}

	return nodes, rows.Err()
}

// ListActionsByState 列出指定 task 和 state 的 action 节点
func (s *Store) ListActionsByState(ctx context.Context, taskID string, state State) ([]Node, error) {
	query := `
		SELECT id, task_id, kind, content,
		       state, complexity, depends_on, blocked_reason,
		       confidence,
		       priority, owner, source_type, source_id, tags, metadata,
		       created_at, updated_at, completed_at
		FROM wm_node
		WHERE task_id = $1 AND kind = 'action' AND state = $2
		ORDER BY priority DESC, created_at ASC
	`

	rows, err := s.pool.Query(ctx, query, taskID, state)
	if err != nil {
		return nil, fmt.Errorf("list actions by state: %w", err)
	}
	defer rows.Close()

	var nodes []Node
	for rows.Next() {
		var node Node
		err := rows.Scan(
			&node.ID, &node.TaskID, &node.Kind, &node.Content,
			&node.State, &node.Complexity, &node.DependsOn, &node.BlockedReason,
			&node.Confidence,
			&node.Priority, &node.Owner, &node.SourceType, &node.SourceID, &node.Tags, &node.Metadata,
			&node.CreatedAt, &node.UpdatedAt, &node.CompletedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan node: %w", err)
		}
		nodes = append(nodes, node)
	}

	return nodes, rows.Err()
}

// UpdateActionState 更新 action 的状态
func (s *Store) UpdateActionState(ctx context.Context, id string, state State, blockedReason *string) error {
	query := `
		UPDATE wm_node
		SET state = $2, blocked_reason = $3, updated_at = now(),
		    completed_at = CASE WHEN $2 IN ('done', 'failed', 'exhausted', 'aborted') THEN now() ELSE NULL END
		WHERE id = $1 AND kind = 'action'
	`

	tag, err := s.pool.Exec(ctx, query, id, state, blockedReason)
	if err != nil {
		return fmt.Errorf("update action state: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("action not found: %s", id)
	}
	return nil
}

// UpdateNodeConfidence 更新 observation/result 的置信度
func (s *Store) UpdateNodeConfidence(ctx context.Context, id string, confidence Confidence) error {
	query := `
		UPDATE wm_node
		SET confidence = $2, updated_at = now()
		WHERE id = $1 AND kind IN ('observation', 'result')
	`

	tag, err := s.pool.Exec(ctx, query, id, confidence)
	if err != nil {
		return fmt.Errorf("update node confidence: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("node not found or not observation/result: %s", id)
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

// ListEdgesByRelation 列出指定 task 和关系类型的所有边
func (s *Store) ListEdgesByRelation(ctx context.Context, taskID string, rel Relation) ([]Edge, error) {
	query := `
		SELECT task_id, src_id, rel, dst_id, attrs, created_at
		FROM wm_edge
		WHERE task_id = $1 AND rel = $2
		ORDER BY created_at ASC
	`

	rows, err := s.pool.Query(ctx, query, taskID, rel)
	if err != nil {
		return nil, fmt.Errorf("list edges by relation: %w", err)
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
		INSERT INTO wm_verification (id, task_id, node_id, primitives, outcome, evaluation, duration_ms, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id
	`

	var id string
	err := s.pool.QueryRow(ctx, query,
		v.ID, v.TaskID, v.NodeID, v.Primitives, v.Outcome, v.Evaluation, v.DurationMs, v.CreatedAt,
	).Scan(&id)

	if err != nil {
		return "", fmt.Errorf("record verification: %w", err)
	}
	return id, nil
}

// GetVerification 获取验证记录
func (s *Store) GetVerification(ctx context.Context, id string) (*Verification, error) {
	query := `
		SELECT id, task_id, node_id, primitives, outcome, evaluation, duration_ms, created_at
		FROM wm_verification
		WHERE id = $1
	`

	var v Verification
	err := s.pool.QueryRow(ctx, query, id).Scan(
		&v.ID, &v.TaskID, &v.NodeID, &v.Primitives, &v.Outcome, &v.Evaluation, &v.DurationMs, &v.CreatedAt,
	)

	if err != nil {
		return nil, fmt.Errorf("get verification: %w", err)
	}
	return &v, nil
}

// ─────────────────────────────────────────────
//  便捷方法
// ─────────────────────────────────────────────

// ListOpenActions 列出待执行的 action（state=open）
func (s *Store) ListOpenActions(ctx context.Context, taskID string) ([]Node, error) {
	return s.ListActionsByState(ctx, taskID, StateOpen)
}

// ListCompletedActions 列出已完成的 action（state=done）
func (s *Store) ListCompletedActions(ctx context.Context, taskID string) ([]Node, error) {
	return s.ListActionsByState(ctx, taskID, StateDone)
}

// ListResults 列出所有发现
func (s *Store) ListResults(ctx context.Context, taskID string) ([]Node, error) {
	return s.ListNodesByKind(ctx, taskID, KindResult)
}

// ListVerifiedResults 列出已验证的发现
func (s *Store) ListVerifiedResults(ctx context.Context, taskID string) ([]Node, error) {
	query := `
		SELECT id, task_id, kind, content,
		       state, complexity, depends_on, blocked_reason,
		       confidence,
		       priority, owner, source_type, source_id, tags, metadata,
		       created_at, updated_at, completed_at
		FROM wm_node
		WHERE task_id = $1 AND kind = 'result' AND confidence = 'verified'
		ORDER BY priority DESC, created_at ASC
	`

	rows, err := s.pool.Query(ctx, query, taskID)
	if err != nil {
		return nil, fmt.Errorf("list verified results: %w", err)
	}
	defer rows.Close()

	var nodes []Node
	for rows.Next() {
		var node Node
		err := rows.Scan(
			&node.ID, &node.TaskID, &node.Kind, &node.Content,
			&node.State, &node.Complexity, &node.DependsOn, &node.BlockedReason,
			&node.Confidence,
			&node.Priority, &node.Owner, &node.SourceType, &node.SourceID, &node.Tags, &node.Metadata,
			&node.CreatedAt, &node.UpdatedAt, &node.CompletedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan node: %w", err)
		}
		nodes = append(nodes, node)
	}

	return nodes, rows.Err()
}

// ListHypotheses 列出所有假设
func (s *Store) ListHypotheses(ctx context.Context, taskID string) ([]Node, error) {
	return s.ListNodesByKind(ctx, taskID, KindObservation)
}

// ListUnverifiedHypotheses 列出未验证的假设
func (s *Store) ListUnverifiedHypotheses(ctx context.Context, taskID string) ([]Node, error) {
	query := `
		SELECT id, task_id, kind, content,
		       state, complexity, depends_on, blocked_reason,
		       confidence,
		       priority, owner, source_type, source_id, tags, metadata,
		       created_at, updated_at, completed_at
		FROM wm_node
		WHERE task_id = $1 AND kind = 'observation' AND confidence = 'unverified'
		ORDER BY priority DESC, created_at ASC
	`

	rows, err := s.pool.Query(ctx, query, taskID)
	if err != nil {
		return nil, fmt.Errorf("list unverified hypotheses: %w", err)
	}
	defer rows.Close()

	var nodes []Node
	for rows.Next() {
		var node Node
		err := rows.Scan(
			&node.ID, &node.TaskID, &node.Kind, &node.Content,
			&node.State, &node.Complexity, &node.DependsOn, &node.BlockedReason,
			&node.Confidence,
			&node.Priority, &node.Owner, &node.SourceType, &node.SourceID, &node.Tags, &node.Metadata,
			&node.CreatedAt, &node.UpdatedAt, &node.CompletedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan node: %w", err)
		}
		nodes = append(nodes, node)
	}

	return nodes, rows.Err()
}

// ─────────────────────────────────────────────
//  Orchestrator & Monitor 需要的查询方法
// ─────────────────────────────────────────────

// ListAllActions 列出所有 Actions（包括 open/running/done/aborted）
func (s *Store) ListAllActions(ctx context.Context, taskID string) ([]Node, error) {
	return s.ListNodesByKind(ctx, taskID, KindAction)
}

// ListRunningActions 列出正在运行的 Actions
func (s *Store) ListRunningActions(ctx context.Context, taskID string) ([]Node, error) {
	query := `
		SELECT id, task_id, kind, content,
		       state, complexity, depends_on, blocked_reason,
		       confidence,
		       priority, owner, source_type, source_id, tags, metadata,
		       created_at, updated_at, completed_at
		FROM wm_node
		WHERE task_id = $1 AND kind = 'action' AND state = 'running'
		ORDER BY priority DESC, created_at ASC
	`

	rows, err := s.pool.Query(ctx, query, taskID)
	if err != nil {
		return nil, fmt.Errorf("list running actions: %w", err)
	}
	defer rows.Close()

	var nodes []Node
	for rows.Next() {
		var node Node
		err := rows.Scan(
			&node.ID, &node.TaskID, &node.Kind, &node.Content,
			&node.State, &node.Complexity, &node.DependsOn, &node.BlockedReason,
			&node.Confidence,
			&node.Priority, &node.Owner, &node.SourceType, &node.SourceID, &node.Tags, &node.Metadata,
			&node.CreatedAt, &node.UpdatedAt, &node.CompletedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan node: %w", err)
		}
		nodes = append(nodes, node)
	}

	return nodes, rows.Err()
}

// GetObjective 获取任务目标（从 objective 节点）
func (s *Store) GetObjective(ctx context.Context, taskID string) (ObjectiveNode, error) {
	objectives, err := s.ListNodesByKind(ctx, taskID, KindObjective)
	if err != nil {
		return ObjectiveNode{}, fmt.Errorf("list objectives: %w", err)
	}

	if len(objectives) == 0 {
		return ObjectiveNode{}, fmt.Errorf("no objective found for task %s", taskID)
	}

	// 取第一个 objective（一个 task 通常只有一个 objective）
	obj := objectives[0]

	// 解析 content 获取 goal
	var content struct {
		Goal string `json:"goal"`
	}
	if err := json.Unmarshal(obj.Content, &content); err != nil {
		return ObjectiveNode{}, fmt.Errorf("parse objective content: %w", err)
	}

	return ObjectiveNode{
		ID:   obj.ID,
		Goal: content.Goal,
	}, nil
}

// ListResults 列出所有 results（从 result 节点）

// FindVerifiedObservation 查找已验证的 observation（标题匹配）
func (s *Store) FindVerifiedObservation(ctx context.Context, taskID, title string) (*Node, error) {
	query := `
		SELECT id, task_id, kind, content,
		       state, complexity, depends_on, blocked_reason,
		       confidence,
		       priority, owner, source_type, source_id, tags, metadata,
		       created_at, updated_at, completed_at
		FROM wm_node
		WHERE task_id = $1
		  AND kind = 'observation'
		  AND confidence = 'verified'
		  AND content::jsonb->>'statement' ILIKE '%' || $2 || '%'
		ORDER BY created_at DESC
		LIMIT 1
	`

	var node Node
	err := s.pool.QueryRow(ctx, query, taskID, title).Scan(
		&node.ID, &node.TaskID, &node.Kind, &node.Content,
		&node.State, &node.Complexity, &node.DependsOn, &node.BlockedReason,
		&node.Confidence,
		&node.Priority, &node.Owner, &node.SourceType, &node.SourceID, &node.Tags, &node.Metadata,
		&node.CreatedAt, &node.UpdatedAt, &node.CompletedAt,
	)

	if err != nil {
		if err.Error() == "no rows in result set" {
			return nil, nil // 未找到
		}
		return nil, fmt.Errorf("find verified observation: %w", err)
	}

	return &node, nil
}

// CompareAndSwapState 原子地更新 Node 状态（CAS - Compare And Swap）
//
// 使用乐观锁机制，只有当前状态匹配 expectedState 时才更新为 newState。
//
// 参数：
// - nodeID: 节点 ID
// - expectedState: 期望的当前状态
// - newState: 新状态
//
// 返回：
// - bool: true = 更新成功，false = 状态已被其他实例修改
// - error: 数据库错误
//
// 使用场景：
// - Orchestrator 执行前：CAS(action_id, open, running)
// - Monitor kill：CAS(action_id, running, aborted)
//
// 示例：
//   success, err := store.CompareAndSwapState(ctx, actionID, StateOpen, StateRunning)
//   if !success {
//       // 状态已被其他实例修改，跳过执行
//   }
func (s *Store) CompareAndSwapState(
	ctx context.Context,
	nodeID string,
	expectedState State,
	newState State,
) (bool, error) {
	query := `
		UPDATE wm_node
		SET state = $1,
		    version = version + 1,
		    updated_at = NOW()
		WHERE id = $2
		  AND state = $3
	`

	result, err := s.pool.Exec(ctx, query, newState, nodeID, expectedState)
	if err != nil {
		return false, fmt.Errorf("CAS update failed: %w", err)
	}

	rowsAffected := result.RowsAffected()
	return rowsAffected == 1, nil
}

// CompareAndSwapStateWithMetadata 原子地更新状态并附加 metadata
//
// 与 CompareAndSwapState 类似，但同时更新 metadata 字段。
//
// 使用场景：
// - Monitor kill 时记录原因
func (s *Store) CompareAndSwapStateWithMetadata(
	ctx context.Context,
	nodeID string,
	expectedState State,
	newState State,
	metadata json.RawMessage,
) (bool, error) {
	query := `
		UPDATE wm_node
		SET state = $1,
		    metadata = $2,
		    version = version + 1,
		    updated_at = NOW()
		WHERE id = $3
		  AND state = $4
	`

	result, err := s.pool.Exec(ctx, query, newState, metadata, nodeID, expectedState)
	if err != nil {
		return false, fmt.Errorf("CAS update with metadata failed: %w", err)
	}

	rowsAffected := result.RowsAffected()
	return rowsAffected == 1, nil
}

// HasPendingAction 检查是否存在相同的未完成 Action
//
// 去重策略：只检查 state IN ('open', 'running') 的 Actions
// 允许：重新扫描已完成的 Actions（done/failed/aborted）
//
// 参数：
// - taskID: 任务 ID
// - fingerprint: Action 内容指纹（SHA256）
//
// 返回：
// - bool: true = 存在相同的未完成 Action
// - error: 数据库错误
//
// 使用场景：
// - Planner 创建 Action 前检查
// - 防止重复规划
//
// 示例：
//   hasPending, _ := store.HasPendingAction(ctx, taskID, fingerprint)
//   if hasPending {
//       // 跳过：已有相同的未完成 Action
//   }
func (s *Store) HasPendingAction(
	ctx context.Context,
	taskID string,
	fingerprint string,
) (bool, error) {
	query := `
		SELECT EXISTS(
			SELECT 1
			FROM wm_node
			WHERE task_id = $1
			  AND kind = 'action'
			  AND fingerprint = $2
			  AND state IN ('open', 'running')
		)
	`

	var exists bool
	err := s.pool.QueryRow(ctx, query, taskID, fingerprint).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check pending action: %w", err)
	}

	return exists, nil
}

// CountActionsByFingerprint 统计指定 fingerprint 的 Actions 数量（按状态分组）
//
// 用于调试和监控：查看某个 Action 被执行了多少次
//
// 返回格式：
// {
//   "open": 1,
//   "running": 0,
//   "done": 5,
//   "failed": 2
// }
func (s *Store) CountActionsByFingerprint(
	ctx context.Context,
	taskID string,
	fingerprint string,
) (map[State]int, error) {
	query := `
		SELECT state, COUNT(*)
		FROM wm_node
		WHERE task_id = $1
		  AND kind = 'action'
		  AND fingerprint = $2
		GROUP BY state
	`

	rows, err := s.pool.Query(ctx, query, taskID, fingerprint)
	if err != nil {
		return nil, fmt.Errorf("count actions by fingerprint: %w", err)
	}
	defer rows.Close()

	counts := make(map[State]int)
	for rows.Next() {
		var state State
		var count int
		if err := rows.Scan(&state, &count); err != nil {
			return nil, err
		}
		counts[state] = count
	}

	return counts, rows.Err()
}
