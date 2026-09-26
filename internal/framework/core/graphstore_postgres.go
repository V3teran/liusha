package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresGraphStore 是基于 PostgreSQL 的 GraphStore 实现。
//
// 表结构映射：
// - GraphNode → wm_node 表
// - GraphEdge → wm_edge 表
//
// 字段映射：
// - GraphNode.ID → wm_node.id
// - GraphNode.Kind → wm_node.kind
// - GraphNode.Content → wm_node.content (JSONB)
// - GraphNode.Metadata → wm_node.metadata (JSONB)
// - GraphNode.State → wm_node.state
// - GraphNode.Confidence → 存储在 metadata["_confidence"] 中（因为表中 confidence 是 TEXT 类型）
// - GraphNode.CreatedAt → wm_node.created_at
// - GraphNode.UpdatedAt → wm_node.updated_at
//
// 注意：wm_node 表有一些业务特定的 NOT NULL 字段（task_id, source_type, source_id），
// 这些字段通过 Metadata 传递，或使用合理的默认值。
type PostgresGraphStore struct {
	pool *pgxpool.Pool
}

// NewPostgresGraphStore 创建新的 PostgreSQL GraphStore。
func NewPostgresGraphStore(pool *pgxpool.Pool) *PostgresGraphStore {
	return &PostgresGraphStore{pool: pool}
}

// CreateNode 创建新节点。
func (s *PostgresGraphStore) CreateNode(ctx context.Context, node *GraphNode) error {
	if node == nil {
		return errors.New("node cannot be nil")
	}
	if node.ID == "" {
		return errors.New("node ID cannot be empty")
	}
	if node.Kind == "" {
		return errors.New("node kind cannot be empty")
	}

	// 从 Metadata 中提取业务字段
	taskID := s.extractString(node.Metadata, "task_id", "default")
	sourceType := s.extractString(node.Metadata, "source_type", "system")
	sourceID := s.extractString(node.Metadata, "source_id", "framework")
	priority := s.extractInt(node.Metadata, "priority", 50)
	owner := s.extractStringPtr(node.Metadata, "owner")

	// 将 GraphNode.Confidence (float64) 存储到 metadata 中
	metadata := s.cloneMetadata(node.Metadata)
	if node.Confidence > 0 {
		metadata["_confidence"] = node.Confidence
	}

	// Action 特定字段
	var complexity *string
	var dependsOn []string
	var blockedReason *string
	var roadmapStep *float64
	if node.Kind == string(KindAction) {
		c := s.extractString(node.Metadata, "complexity", "simple")
		complexity = &c
		dependsOn = s.extractStringArray(node.Metadata, "depends_on")
		blockedReason = s.extractStringPtr(node.Metadata, "blocked_reason")
		roadmapStep = s.extractFloat64Ptr(node.Metadata, "roadmap_step")
	}

	// Observation/Evaluation/Result 特定字段（表中的 confidence 字段）
	var dbConfidence *string
	if node.Kind == string(KindObservation) || node.Kind == string(KindResult) {
		conf := "unverified"
		if node.Confidence >= 0.8 {
			conf = "verified"
		}
		dbConfidence = &conf
	}

	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}

	query := `
		INSERT INTO wm_node (
			id, task_id, kind, content,
			state, complexity, depends_on, blocked_reason, roadmap_step,
			confidence, version,
			priority, owner, source_type, source_id,
			tags, metadata,
			created_at, updated_at
		) VALUES (
			$1, $2, $3, $4,
			$5, $6, $7, $8, $9,
			$10, $11,
			$12, $13, $14, $15,
			$16, $17,
			$18, $19
		)
	`

	now := time.Now()
	if !node.CreatedAt.IsZero() {
		now = node.CreatedAt
	}

	// Version 默认为 1（新节点）
	version := node.Version
	if version == 0 {
		version = 1
	}

	_, err = s.pool.Exec(ctx, query,
		node.ID,
		taskID,
		node.Kind,
		node.Content,
		nullString(node.State),
		complexity,
		dependsOn,
		blockedReason,
		roadmapStep,
		dbConfidence,
		version,
		priority,
		owner,
		sourceType,
		sourceID,
		[]string{}, // tags
		metadataJSON,
		now,
		now,
	)

	if err != nil {
		return fmt.Errorf("insert node: %w", err)
	}

	return nil
}

// GetNode 获取节点。
func (s *PostgresGraphStore) GetNode(ctx context.Context, id string) (*GraphNode, error) {
	if id == "" {
		return nil, errors.New("node ID cannot be empty")
	}

	query := `
		SELECT
			id, kind, content, state, confidence, version,
			metadata, created_at, updated_at
		FROM wm_node
		WHERE id = $1
	`

	var node GraphNode
	var state, dbConfidence *string
	var metadataJSON []byte

	err := s.pool.QueryRow(ctx, query, id).Scan(
		&node.ID,
		&node.Kind,
		&node.Content,
		&state,
		&dbConfidence,
		&node.Version,
		&metadataJSON,
		&node.CreatedAt,
		&node.UpdatedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrGraphNodeNotFound
		}
		return nil, fmt.Errorf("query node: %w", err)
	}

	// 解析 metadata
	if len(metadataJSON) > 0 {
		if err := json.Unmarshal(metadataJSON, &node.Metadata); err != nil {
			return nil, fmt.Errorf("unmarshal metadata: %w", err)
		}
	}
	if node.Metadata == nil {
		node.Metadata = make(map[string]interface{})
	}

	// 恢复字段
	if state != nil {
		node.State = *state
	}

	// 从 metadata 中恢复 confidence
	if conf, ok := node.Metadata["_confidence"].(float64); ok {
		node.Confidence = conf
	} else if dbConfidence != nil && *dbConfidence == "verified" {
		node.Confidence = 1.0
	} else if dbConfidence != nil && *dbConfidence == "unverified" {
		node.Confidence = 0.5
	}

	return &node, nil
}

// UpdateNode 更新节点。
func (s *PostgresGraphStore) UpdateNode(ctx context.Context, id string, update GraphNodeUpdate) error {
	if id == "" {
		return errors.New("node ID cannot be empty")
	}

	// 构建动态 UPDATE 语句
	setParts := []string{}
	args := []interface{}{}
	argIndex := 1

	if update.Content != nil {
		setParts = append(setParts, fmt.Sprintf("content = $%d", argIndex))
		args = append(args, update.Content)
		argIndex++
	}

	if update.State != "" {
		setParts = append(setParts, fmt.Sprintf("state = $%d", argIndex))
		args = append(args, update.State)
		argIndex++
	}

	if update.Metadata != nil {
		metadataJSON, err := json.Marshal(update.Metadata)
		if err != nil {
			return fmt.Errorf("marshal metadata: %w", err)
		}
		setParts = append(setParts, fmt.Sprintf("metadata = $%d", argIndex))
		args = append(args, metadataJSON)
		argIndex++
	}

	if update.Confidence != nil {
		// 更新 metadata 中的 _confidence
		// 注意：这里需要先读取当前 metadata，然后更新
		// 为了简化，我们使用 JSONB 操作符
		setParts = append(setParts, fmt.Sprintf("metadata = metadata || $%d::jsonb", argIndex))
		confidenceJSON := fmt.Sprintf(`{"_confidence": %f}`, *update.Confidence)
		args = append(args, confidenceJSON)
		argIndex++
	}

	if len(setParts) == 0 {
		return errors.New("no fields to update")
	}

	// 始终更新 updated_at（由触发器自动更新，但这里保持兼容性）
	setParts = append(setParts, fmt.Sprintf("updated_at = $%d", argIndex))
	args = append(args, time.Now())
	argIndex++

	// 构建 WHERE 条件：id + 可选的 version 检查
	whereParts := []string{fmt.Sprintf("id = $%d", argIndex)}
	args = append(args, id)
	argIndex++

	if update.ExpectedVersion != nil {
		// 乐观锁：只有当前 version 匹配时才更新
		whereParts = append(whereParts, fmt.Sprintf("version = $%d", argIndex))
		args = append(args, *update.ExpectedVersion)
		argIndex++
	}

	query := fmt.Sprintf(`
		UPDATE wm_node
		SET %s
		WHERE %s
	`, strings.Join(setParts, ", "), strings.Join(whereParts, " AND "))

	result, err := s.pool.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("update node: %w", err)
	}

	if result.RowsAffected() == 0 {
		// 区分两种情况：节点不存在 vs 版本不匹配
		if update.ExpectedVersion != nil {
			// 检查节点是否存在
			var exists bool
			checkQuery := `SELECT EXISTS(SELECT 1 FROM wm_node WHERE id = $1)`
			err := s.pool.QueryRow(ctx, checkQuery, id).Scan(&exists)
			if err != nil {
				return fmt.Errorf("check node existence: %w", err)
			}
			if !exists {
				return ErrGraphNodeNotFound
			}
			// 节点存在但版本不匹配
			return ErrVersionMismatch
		}
		return ErrGraphNodeNotFound
	}

	return nil
}

// DeleteNode 删除节点及其所有边。
func (s *PostgresGraphStore) DeleteNode(ctx context.Context, id string) error {
	if id == "" {
		return errors.New("node ID cannot be empty")
	}

	// 删除节点（CASCADE 会自动删除相关的边）
	query := `DELETE FROM wm_node WHERE id = $1`

	result, err := s.pool.Exec(ctx, query, id)
	if err != nil {
		return fmt.Errorf("delete node: %w", err)
	}

	if result.RowsAffected() == 0 {
		return ErrGraphNodeNotFound
	}

	return nil
}

// ListNodes 查询节点列表。
func (s *PostgresGraphStore) ListNodes(ctx context.Context, query GraphNodeQuery) ([]*GraphNode, error) {
	// 构建动态查询
	whereParts := []string{}
	args := []interface{}{}
	argIndex := 1

	if query.Kind != "" {
		whereParts = append(whereParts, fmt.Sprintf("kind = $%d", argIndex))
		args = append(args, query.Kind)
		argIndex++
	}

	if query.State != "" {
		whereParts = append(whereParts, fmt.Sprintf("state = $%d", argIndex))
		args = append(args, query.State)
		argIndex++
	}

	if query.MinConfidence > 0 {
		// 需要从 metadata 中提取 _confidence
		whereParts = append(whereParts, fmt.Sprintf(
			"(metadata->>'_confidence')::float >= $%d", argIndex))
		args = append(args, query.MinConfidence)
		argIndex++
	}

	// Filters（自定义过滤条件）
	for key, value := range query.Filters {
		// 支持 metadata.xxx 路径
		if strings.HasPrefix(key, "metadata.") {
			field := strings.TrimPrefix(key, "metadata.")
			whereParts = append(whereParts, fmt.Sprintf(
				"metadata->>'%s' = $%d", field, argIndex))
			args = append(args, fmt.Sprint(value))
			argIndex++
		}
	}

	whereClause := ""
	if len(whereParts) > 0 {
		whereClause = "WHERE " + strings.Join(whereParts, " AND ")
	}

	// 排序
	orderBy := "created_at DESC"
	if query.OrderBy != "" {
		if strings.HasPrefix(query.OrderBy, "-") {
			orderBy = strings.TrimPrefix(query.OrderBy, "-") + " DESC"
		} else {
			orderBy = query.OrderBy + " ASC"
		}
	}

	// 分页
	limitClause := ""
	if query.Limit > 0 {
		limitClause = fmt.Sprintf("LIMIT $%d", argIndex)
		args = append(args, query.Limit)
		argIndex++
	}

	offsetClause := ""
	if query.Offset > 0 {
		offsetClause = fmt.Sprintf("OFFSET $%d", argIndex)
		args = append(args, query.Offset)
		argIndex++
	}

	sqlQuery := fmt.Sprintf(`
		SELECT
			id, kind, content, state, confidence, version,
			metadata, created_at, updated_at
		FROM wm_node
		%s
		ORDER BY %s
		%s %s
	`, whereClause, orderBy, limitClause, offsetClause)

	rows, err := s.pool.Query(ctx, sqlQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("query nodes: %w", err)
	}
	defer rows.Close()

	var nodes []*GraphNode
	for rows.Next() {
		var node GraphNode
		var state, dbConfidence *string
		var metadataJSON []byte

		err := rows.Scan(
			&node.ID,
			&node.Kind,
			&node.Content,
			&state,
			&dbConfidence,
			&node.Version,
			&metadataJSON,
			&node.CreatedAt,
			&node.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan node: %w", err)
		}

		// 解析 metadata
		if len(metadataJSON) > 0 {
			if err := json.Unmarshal(metadataJSON, &node.Metadata); err != nil {
				return nil, fmt.Errorf("unmarshal metadata: %w", err)
			}
		}
		if node.Metadata == nil {
			node.Metadata = make(map[string]interface{})
		}

		// 恢复字段
		if state != nil {
			node.State = *state
		}

		// 从 metadata 中恢复 confidence
		if conf, ok := node.Metadata["_confidence"].(float64); ok {
			node.Confidence = conf
		} else if dbConfidence != nil && *dbConfidence == "verified" {
			node.Confidence = 1.0
		} else if dbConfidence != nil && *dbConfidence == "unverified" {
			node.Confidence = 0.5
		}

		nodes = append(nodes, &node)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate nodes: %w", err)
	}

	return nodes, nil
}

// CompareAndSwapState 原子更新节点状态（使用乐观锁）
func (s *PostgresGraphStore) CompareAndSwapState(ctx context.Context, taskID, nodeID string, expectedState, newState string) (bool, error) {
	if nodeID == "" {
		return false, errors.New("node ID cannot be empty")
	}
	if expectedState == "" || newState == "" {
		return false, errors.New("expectedState and newState cannot be empty")
	}

	// 先读取当前节点获取 version
	node, err := s.GetNode(ctx, nodeID)
	if err != nil {
		return false, err
	}

	// 检查 taskID（如果提供）
	if taskID != "" {
		if node.Metadata != nil {
			if tid, ok := node.Metadata["task_id"].(string); ok && tid != taskID {
				return false, nil // 不属于该任务
			}
		}
	}

	// 检查状态是否匹配
	if node.State != expectedState {
		// 状态不匹配，CAS 失败
		return false, nil
	}

	// 使用乐观锁更新状态
	query := `
		UPDATE wm_node
		SET state = $1, updated_at = $2
		WHERE id = $3 AND version = $4
	`

	result, err := s.pool.Exec(ctx, query, newState, time.Now(), nodeID, node.Version)
	if err != nil {
		return false, fmt.Errorf("update state: %w", err)
	}

	// 如果没有更新任何行，说明 version 已经变化（被其他并发操作修改）
	if result.RowsAffected() == 0 {
		return false, nil
	}

	return true, nil
}

// CreateEdge 创建边（幂等）。
func (s *PostgresGraphStore) CreateEdge(ctx context.Context, edge *GraphEdge) error {
	if edge == nil {
		return errors.New("edge cannot be nil")
	}
	if edge.From == "" || edge.To == "" {
		return errors.New("edge from/to cannot be empty")
	}
	if edge.Relation == "" {
		return errors.New("edge relation cannot be empty")
	}

	// 从 From 节点获取 task_id
	taskID, err := s.getNodeTaskID(ctx, edge.From)
	if err != nil {
		return fmt.Errorf("get task_id from node %s: %w", edge.From, err)
	}

	attrsJSON, err := json.Marshal(edge.Metadata)
	if err != nil {
		return fmt.Errorf("marshal edge metadata: %w", err)
	}

	query := `
		INSERT INTO wm_edge (task_id, src_id, rel, dst_id, attrs, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (task_id, src_id, rel, dst_id) DO NOTHING
	`

	now := time.Now()
	if !edge.CreatedAt.IsZero() {
		now = edge.CreatedAt
	}

	_, err = s.pool.Exec(ctx, query,
		taskID,
		edge.From,
		edge.Relation,
		edge.To,
		attrsJSON,
		now,
	)

	if err != nil {
		return fmt.Errorf("insert edge: %w", err)
	}

	return nil
}

// ListEdges 查询边列表。
func (s *PostgresGraphStore) ListEdges(ctx context.Context, query GraphEdgeQuery) ([]*GraphEdge, error) {
	whereParts := []string{}
	args := []interface{}{}
	argIndex := 1

	if query.From != "" {
		whereParts = append(whereParts, fmt.Sprintf("src_id = $%d", argIndex))
		args = append(args, query.From)
		argIndex++
	}

	if query.To != "" {
		whereParts = append(whereParts, fmt.Sprintf("dst_id = $%d", argIndex))
		args = append(args, query.To)
		argIndex++
	}

	if query.Relation != "" {
		whereParts = append(whereParts, fmt.Sprintf("rel = $%d", argIndex))
		args = append(args, query.Relation)
		argIndex++
	}

	whereClause := ""
	if len(whereParts) > 0 {
		whereClause = "WHERE " + strings.Join(whereParts, " AND ")
	}

	limitClause := ""
	if query.Limit > 0 {
		limitClause = fmt.Sprintf("LIMIT $%d", argIndex)
		args = append(args, query.Limit)
		argIndex++
	}

	sqlQuery := fmt.Sprintf(`
		SELECT src_id, dst_id, rel, attrs, created_at
		FROM wm_edge
		%s
		ORDER BY created_at DESC
		%s
	`, whereClause, limitClause)

	rows, err := s.pool.Query(ctx, sqlQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("query edges: %w", err)
	}
	defer rows.Close()

	var edges []*GraphEdge
	for rows.Next() {
		var edge GraphEdge
		var attrsJSON []byte

		err := rows.Scan(
			&edge.From,
			&edge.To,
			&edge.Relation,
			&attrsJSON,
			&edge.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan edge: %w", err)
		}

		// 解析 metadata
		if len(attrsJSON) > 0 {
			if err := json.Unmarshal(attrsJSON, &edge.Metadata); err != nil {
				return nil, fmt.Errorf("unmarshal edge metadata: %w", err)
			}
		}

		edges = append(edges, &edge)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate edges: %w", err)
	}

	return edges, nil
}

// DeleteEdge 删除边。
func (s *PostgresGraphStore) DeleteEdge(ctx context.Context, from, to, relation string) error {
	if from == "" || to == "" || relation == "" {
		return errors.New("from/to/relation cannot be empty")
	}

	query := `
		DELETE FROM wm_edge
		WHERE src_id = $1 AND dst_id = $2 AND rel = $3
	`

	result, err := s.pool.Exec(ctx, query, from, to, relation)
	if err != nil {
		return fmt.Errorf("delete edge: %w", err)
	}

	if result.RowsAffected() == 0 {
		return ErrGraphEdgeNotFound
	}

	return nil
}

// Traverse 图遍历（BFS/DFS）。
func (s *PostgresGraphStore) Traverse(ctx context.Context, startID string, query GraphTraverseQuery) ([]*GraphNode, error) {
	if startID == "" {
		return nil, errors.New("start node ID cannot be empty")
	}

	// 设置默认值
	if query.Direction == "" {
		query.Direction = GraphTraverseOut
	}
	if query.Strategy == "" {
		query.Strategy = GraphTraverseBFS
	}

	// 使用迭代实现遍历（避免递归深度限制）
	visited := make(map[string]bool)
	var result []*GraphNode
	queue := []string{startID}

	depth := 0
	for len(queue) > 0 {
		// 检查深度限制
		if query.MaxDepth > 0 && depth >= query.MaxDepth {
			break
		}

		// BFS: 取队列头；DFS: 取队列尾
		var currentID string
		if query.Strategy == GraphTraverseBFS {
			currentID = queue[0]
			queue = queue[1:]
		} else {
			currentID = queue[len(queue)-1]
			queue = queue[:len(queue)-1]
		}

		// 跳过已访问节点
		if visited[currentID] {
			continue
		}
		visited[currentID] = true

		// 获取当前节点
		node, err := s.GetNode(ctx, currentID)
		if err != nil {
			if errors.Is(err, ErrGraphNodeNotFound) {
				continue
			}
			return nil, fmt.Errorf("get node %s: %w", currentID, err)
		}

		// 应用节点过滤器
		if query.NodeFilter != nil {
			if !s.matchNodeFilter(node, *query.NodeFilter) {
				continue
			}
		}

		result = append(result, node)

		// 获取邻居节点
		neighbors, err := s.getNeighbors(ctx, currentID, query.Direction, query.Relations)
		if err != nil {
			return nil, fmt.Errorf("get neighbors of %s: %w", currentID, err)
		}

		// 添加到队列
		queue = append(queue, neighbors...)

		depth++
	}

	return result, nil
}

// getNeighbors 获取节点的邻居。
func (s *PostgresGraphStore) getNeighbors(ctx context.Context, nodeID string, direction GraphTraverseDirection, relations []string) ([]string, error) {
	var query string
	var args []interface{}

	// 构建关系过滤条件
	relFilter := ""
	if len(relations) > 0 {
		relPlaceholders := make([]string, len(relations))
		argOffset := 2 // 从 $2 开始
		for i := range relations {
			relPlaceholders[i] = fmt.Sprintf("$%d", argOffset+i)
		}
		relFilter = fmt.Sprintf(" AND rel IN (%s)", strings.Join(relPlaceholders, ","))
	}

	switch direction {
	case GraphTraverseOut:
		query = fmt.Sprintf(`SELECT dst_id FROM wm_edge WHERE src_id = $1%s`, relFilter)
		args = []interface{}{nodeID}
	case GraphTraverseIn:
		query = fmt.Sprintf(`SELECT src_id FROM wm_edge WHERE dst_id = $1%s`, relFilter)
		args = []interface{}{nodeID}
	case GraphTraverseBoth:
		query = fmt.Sprintf(`
			SELECT dst_id FROM wm_edge WHERE src_id = $1%s
			UNION
			SELECT src_id FROM wm_edge WHERE dst_id = $2%s
		`, relFilter, relFilter)
		args = []interface{}{nodeID, nodeID}
	default:
		return nil, fmt.Errorf("invalid traverse direction: %s", direction)
	}

	// 添加关系过滤参数
	if len(relations) > 0 {
		for _, rel := range relations {
			args = append(args, rel)
		}
		// 对于 GraphTraverseBoth，需要再添加一次参数
		if direction == GraphTraverseBoth {
			for _, rel := range relations {
				args = append(args, rel)
			}
		}
	}

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query neighbors: %w", err)
	}
	defer rows.Close()

	var neighbors []string
	for rows.Next() {
		var neighborID string
		if err := rows.Scan(&neighborID); err != nil {
			return nil, fmt.Errorf("scan neighbor: %w", err)
		}
		neighbors = append(neighbors, neighborID)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate neighbors: %w", err)
	}

	return neighbors, nil
}

// matchNodeFilter 检查节点是否匹配过滤器。
func (s *PostgresGraphStore) matchNodeFilter(node *GraphNode, filter GraphNodeQuery) bool {
	if filter.Kind != "" && node.Kind != filter.Kind {
		return false
	}
	if filter.State != "" && node.State != filter.State {
		return false
	}
	if filter.MinConfidence > 0 && node.Confidence < filter.MinConfidence {
		return false
	}
	return true
}

// getNodeTaskID 获取节点的 task_id。
func (s *PostgresGraphStore) getNodeTaskID(ctx context.Context, nodeID string) (string, error) {
	var taskID string
	err := s.pool.QueryRow(ctx, `SELECT task_id FROM wm_node WHERE id = $1`, nodeID).Scan(&taskID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrGraphNodeNotFound
		}
		return "", err
	}
	return taskID, nil
}

// 辅助函数

func (s *PostgresGraphStore) extractString(m map[string]interface{}, key, defaultValue string) string {
	if m == nil {
		return defaultValue
	}
	if v, ok := m[key].(string); ok {
		return v
	}
	return defaultValue
}

func (s *PostgresGraphStore) extractStringPtr(m map[string]interface{}, key string) *string {
	if m == nil {
		return nil
	}
	if v, ok := m[key].(string); ok {
		return &v
	}
	return nil
}

func (s *PostgresGraphStore) extractFloat64Ptr(m map[string]interface{}, key string) *float64 {
	if m == nil {
		return nil
	}
	if v, ok := m[key].(float64); ok {
		return &v
	}
	if v, ok := m[key].(float32); ok {
		f := float64(v)
		return &f
	}
	if v, ok := m[key].(int); ok {
		f := float64(v)
		return &f
	}
	return nil
}

func (s *PostgresGraphStore) extractInt(m map[string]interface{}, key string, defaultValue int) int {
	if m == nil {
		return defaultValue
	}
	if v, ok := m[key].(int); ok {
		return v
	}
	if v, ok := m[key].(float64); ok {
		return int(v)
	}
	return defaultValue
}

func (s *PostgresGraphStore) extractStringArray(m map[string]interface{}, key string) []string {
	if m == nil {
		return nil
	}
	if v, ok := m[key].([]string); ok {
		return v
	}
	if v, ok := m[key].([]interface{}); ok {
		result := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
		return result
	}
	return nil
}

func (s *PostgresGraphStore) cloneMetadata(m map[string]interface{}) map[string]interface{} {
	if m == nil {
		return make(map[string]interface{})
	}
	result := make(map[string]interface{}, len(m))
	for k, v := range m {
		result[k] = v
	}
	return result
}

func nullString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
