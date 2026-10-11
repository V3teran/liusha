package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresGraphStore 是基于 PostgreSQL 的 GraphStore 实现。
//
// 表结构映射：
// - GraphNode → exploration_node 表
// - GraphEdge → exploration_edge 表
//
// 字段映射（0157 定稿后仅保留 9 列，唯一事实源 = content + metadata）：
// - GraphNode.ID → exploration_node.id (uuid)
// - GraphNode.TaskID（经 Metadata["task_id"]）→ exploration_node.task_id (uuid，查询维度)
// - GraphNode.Kind → exploration_node.kind（查询维度）
// - GraphNode.Content → exploration_node.content (JSONB)
// - GraphNode.Metadata → exploration_node.metadata (JSONB，含 confidence 枚举等扩展位)
// - GraphNode.State → exploration_node.state（查询维度）
// - GraphNode.CreatedAt/UpdatedAt/Version → 同名列
//
// confidence 是验证生命周期枚举（unverified/verified/refuted），以枚举原文存
// metadata["confidence"]——由复现门独占写权流转，非置信度数值。
type PostgresGraphStore struct {
	pool      *pgxpool.Pool
	nodeTable string // 节点表名（默认 exploration_node）
	edgeTable string // 边表名（默认 exploration_edge）
}

// 默认表名（liusha 探索图业务的 schema）。
const (
	defaultNodeTable = "exploration_node"
	defaultEdgeTable = "exploration_edge"
)

// NewPostgresGraphStore 创建新的 PostgreSQL GraphStore（默认探索图表名）。
func NewPostgresGraphStore(pool *pgxpool.Pool) *PostgresGraphStore {
	return &PostgresGraphStore{pool: pool, nodeTable: defaultNodeTable, edgeTable: defaultEdgeTable}
}

// execer 抽象 pool 与 tx 共有的 Exec 能力，让节点/边 INSERT 可在事务内外复用。
type execer interface {
	Exec(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error)
}

// CreateNode 创建新节点。
func (s *PostgresGraphStore) CreateNode(ctx context.Context, node *GraphNode) error {
	return s.insertNode(ctx, s.pool, node)
}

func (s *PostgresGraphStore) insertNode(ctx context.Context, db execer, node *GraphNode) error {
	if node == nil {
		return errors.New("node cannot be nil")
	}
	if node.ID == "" {
		return errors.New("node ID cannot be empty")
	}
	if node.Kind == "" {
		return errors.New("node kind cannot be empty")
	}

	// task_id 是唯一的列级归属维度（经 metadata 传递，见字段映射）
	taskID := s.extractString(node.Metadata, "task_id", "")
	if taskID == "" {
		return errors.New("node metadata.task_id is required")
	}

	metadataJSON, err := json.Marshal(node.Metadata)
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}

	query := `
		INSERT INTO ` + s.nodeTable + ` (
			id, task_id, kind, content,
			state, version,
			metadata,
			created_at, updated_at
		) VALUES (
			$1, $2, $3, $4,
			$5, $6,
			$7,
			$8, $9
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

	_, err = db.Exec(ctx, query,
		node.ID,
		taskID,
		node.Kind,
		node.Content,
		nullString(node.State),
		version,
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
			id, kind, content, state, version,
			metadata, created_at, updated_at
		FROM ` + s.nodeTable + `
		WHERE id = $1
	`

	var node GraphNode
	var state *string
	var metadataJSON []byte

	err := s.pool.QueryRow(ctx, query, id).Scan(
		&node.ID,
		&node.Kind,
		&node.Content,
		&state,
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

	if state != nil {
		node.State = *state
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
		// argIndex++ 是最后一个参数，无需递增
	}

	query := fmt.Sprintf(`
		UPDATE `+s.nodeTable+`
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
			checkQuery := "SELECT EXISTS(SELECT 1 FROM " + s.nodeTable + " WHERE id = $1)"
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
	query := "DELETE FROM " + s.nodeTable + " WHERE id = $1"

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
	// 1. 构建 SQL 查询
	sqlQuery, args := s.buildListNodesSQL(query)

	// 2. 执行查询并返回结果
	return executeListNodesQuery(ctx, s.pool, sqlQuery, args)
}

// CompareAndSwapState 原子性地比较并交换节点状态。
// 单条 UPDATE 同时承担条件检查与写入（WHERE state = expectedState），
// 不存在读-改-写窗口；version 由数据库触发器在同一语句内自增。
// 返回 (true, nil) 表示更新成功；(false, nil) 表示状态不匹配或节点不存在。
func (s *PostgresGraphStore) CompareAndSwapState(ctx context.Context, taskID, nodeID string, expectedState, newState string) (bool, error) {
	if nodeID == "" {
		return false, errors.New("node ID cannot be empty")
	}
	if expectedState == "" || newState == "" {
		return false, errors.New("expectedState and newState cannot be empty")
	}

	query := `
		UPDATE ` + s.nodeTable + `
		SET state = $1, updated_at = $2
		WHERE id = $3 AND state = $4
	`
	args := []interface{}{newState, time.Now(), nodeID, expectedState}

	if taskID != "" {
		query += ` AND task_id = $5`
		args = append(args, taskID)
	}

	result, err := s.pool.Exec(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("update state: %w", err)
	}

	return result.RowsAffected() > 0, nil
}

// CreateNodeWithEdges 在单个事务内创建节点及其关联边。
func (s *PostgresGraphStore) CreateNodeWithEdges(ctx context.Context, node *GraphNode, edges []*GraphEdge) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := s.insertNode(ctx, tx, node); err != nil {
		return err
	}
	for _, edge := range edges {
		if err := s.insertEdge(ctx, tx, edge); err != nil {
			return err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

// CreateEdge 创建边（幂等）。
func (s *PostgresGraphStore) extractString(m map[string]interface{}, key, defaultValue string) string {
	if m == nil {
		return defaultValue
	}
	if v, ok := m[key].(string); ok {
		return v
	}
	return defaultValue
}

func nullString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
