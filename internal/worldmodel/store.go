package worldmodel

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store 封装攻击图世界模型三表（wm_node/wm_edge/wm_verification）的持久化操作。
type Store struct {
	pool *pgxpool.Pool
}

// NewStore 用 pgxpool 构造 Store。
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// nodeCols 是 SELECT / RETURNING 的统一列序，与 scanNode 一一对应。
const nodeCols = "id, task_id, kind, domain, ref_kind, locator, attrs, " +
	"confidence, verified_by, seq, created_at, updated_at"

// UpsertNode 幂等写入一个节点：同图内 (kind,domain,ref_kind,locator) 冲突则合并 attrs / 提升 confidence。
// 显式稳定键取代 finding 的「summary 前 60 字」脆弱 dedup。
func (s *Store) UpsertNode(ctx context.Context, n Node) (Node, error) {
	if n.TaskID == "" {
		return Node{}, fmt.Errorf("worldmodel: node.TaskID 必填")
	}
	if n.Ref.Domain == "" || n.Ref.RefKind == "" || n.Ref.Locator == "" {
		return Node{}, fmt.Errorf("worldmodel: node.Ref 三元组必填 (domain/ref_kind/locator)")
	}
	attrs := n.Attrs
	if len(attrs) == 0 {
		attrs = json.RawMessage("{}")
	}
	conf := n.Confidence
	if conf == "" {
		conf = ConfAssumed
	}

	const q = `
		INSERT INTO wm_node (task_id, kind, domain, ref_kind, locator, attrs, confidence, verified_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (task_id, kind, domain, ref_kind, locator) DO UPDATE
		SET attrs       = wm_node.attrs || EXCLUDED.attrs,
		    confidence  = CASE WHEN EXCLUDED.confidence = 'confirmed'
		                       THEN 'confirmed' ELSE wm_node.confidence END,
		    verified_by = COALESCE(EXCLUDED.verified_by, wm_node.verified_by),
		    updated_at  = now()
		RETURNING ` + nodeCols
	row := s.pool.QueryRow(ctx, q,
		n.TaskID, n.Kind, n.Ref.Domain, n.Ref.RefKind, n.Ref.Locator, attrs, conf, n.VerifiedBy)
	return scanNode(row)
}

// LinkEdge 幂等连边（同图内 (rel,src,dst) 唯一）。
func (s *Store) LinkEdge(ctx context.Context, e Edge) error {
	if e.TaskID == "" || e.Src == "" || e.Dst == "" {
		return fmt.Errorf("worldmodel: edge.TaskID/Src/Dst 必填")
	}
	attrs := e.Attrs
	if len(attrs) == 0 {
		attrs = json.RawMessage("{}")
	}
	const q = `
		INSERT INTO wm_edge (task_id, rel, src, dst, attrs)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (task_id, rel, src, dst) DO NOTHING`
	_, err := s.pool.Exec(ctx, q, e.TaskID, e.Rel, e.Src, e.Dst, attrs)
	if err != nil {
		return fmt.Errorf("worldmodel: 连边失败: %w", err)
	}
	return nil
}

// RecordVerification 写一条 Verifier 取证记录，返回其 id（供晋升节点的 verified_by 引用）。
func (s *Store) RecordVerification(ctx context.Context, v Verification) (string, error) {
	prims := v.Primitives
	if len(prims) == 0 {
		prims = json.RawMessage("[]")
	}
	ev := v.Evidence
	if len(ev) == 0 {
		ev = json.RawMessage("{}")
	}
	const q = `
		INSERT INTO wm_verification (task_id, lead_id, primitives, outcome, evidence, duration_ms)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id`
	var id string
	if err := s.pool.QueryRow(ctx, q,
		v.TaskID, v.LeadID, prims, v.Outcome, ev, v.DurationMs).Scan(&id); err != nil {
		return "", fmt.Errorf("worldmodel: 记录 verification 失败: %w", err)
	}
	return id, nil
}

// ListNodes 读某图的全部节点（L6 交付/可视化用）。
func (s *Store) ListNodes(ctx context.Context, taskID string) ([]Node, error) {
	const q = "SELECT " + nodeCols + " FROM wm_node WHERE task_id = $1 ORDER BY seq"
	rows, err := s.pool.Query(ctx, q, taskID)
	if err != nil {
		return nil, fmt.Errorf("worldmodel: 查节点失败: %w", err)
	}
	defer rows.Close()
	var out []Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// ListEdges 读某图的全部边。攻击链推理 = 在 enables 边上做路径查找。
func (s *Store) ListEdges(ctx context.Context, taskID string) ([]Edge, error) {
	const q = `SELECT id, task_id, rel, src, dst, attrs, created_at
	           FROM wm_edge WHERE task_id = $1 ORDER BY created_at`
	rows, err := s.pool.Query(ctx, q, taskID)
	if err != nil {
		return nil, fmt.Errorf("worldmodel: 查边失败: %w", err)
	}
	defer rows.Close()
	var out []Edge
	for rows.Next() {
		var e Edge
		if err := rows.Scan(&e.ID, &e.TaskID, &e.Rel, &e.Src, &e.Dst, &e.Attrs, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("worldmodel: 扫描边失败: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ListVerifications 读某图的全部取证记录（L6 交付/审计用）。
// 按 created_at 升序——复现时间线即攻击推进顺序，前端取证面板顺序展示。
func (s *Store) ListVerifications(ctx context.Context, taskID string) ([]Verification, error) {
	const q = `SELECT id, task_id, lead_id, primitives, outcome, evidence, duration_ms, created_at
	           FROM wm_verification WHERE task_id = $1 ORDER BY created_at`
	rows, err := s.pool.Query(ctx, q, taskID)
	if err != nil {
		return nil, fmt.Errorf("worldmodel: 查取证记录失败: %w", err)
	}
	defer rows.Close()
	var out []Verification
	for rows.Next() {
		var v Verification
		if err := rows.Scan(
			&v.ID, &v.TaskID, &v.LeadID, &v.Primitives,
			&v.Outcome, &v.Evidence, &v.DurationMs, &v.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("worldmodel: 扫描取证记录失败: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// rowScanner 抽象 QueryRow / Rows，让 scanNode 两条路径共用。
type rowScanner interface {
	Scan(dest ...any) error
}

func scanNode(row rowScanner) (Node, error) {
	var n Node
	if err := row.Scan(
		&n.ID, &n.TaskID, &n.Kind, &n.Ref.Domain, &n.Ref.RefKind, &n.Ref.Locator,
		&n.Attrs, &n.Confidence, &n.VerifiedBy, &n.Seq, &n.CreatedAt, &n.UpdatedAt,
	); err != nil {
		if err == pgx.ErrNoRows {
			return Node{}, err
		}
		return Node{}, fmt.Errorf("worldmodel: 扫描节点失败: %w", err)
	}
	return n, nil
}

// getNodeByID 按 UUID 主键查单个节点。
func (s *Store) getNodeByID(ctx context.Context, id string) (Node, error) {
	const q = `SELECT ` + nodeCols + ` FROM wm_node WHERE id = $1`
	row := s.pool.QueryRow(ctx, q, id)
	n, err := scanNode(row)
	if err != nil {
		return Node{}, fmt.Errorf("worldmodel: getNodeByID(%s): %w", id, err)
	}
	return n, nil
}

// ────────────────────────────────────────────────────────────────
//  Move CRUD（新增：追踪规划器派发的 Move 执行）
// ────────────────────────────────────────────────────────────────

// moveCols 是 SELECT / RETURNING 的统一列序，与 scanMove 一一对应。
const moveCols = "id, task_id, kind, status, target_node, reason, outcome, created_at, updated_at"

// UpsertMove 幂等写入一个 Move 节点（同 task_id + kind + target_node 视为同一 Move）。
func (s *Store) UpsertMove(ctx context.Context, m Move) (Move, error) {
	if m.TaskID == "" || m.Kind == "" || m.TargetNode == "" {
		return Move{}, fmt.Errorf("worldmodel: Move.TaskID/Kind/TargetNode 必填")
	}
	outcome := m.Outcome
	if len(outcome) == 0 {
		outcome = json.RawMessage("{}")
	}
	status := m.Status
	if status == "" {
		status = MoveStatusPending
	}

	const q = `
		INSERT INTO wm_move (task_id, kind, status, target_node, reason, outcome)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (task_id, kind, target_node) DO UPDATE
		SET status     = EXCLUDED.status,
		    outcome    = EXCLUDED.outcome,
		    updated_at = now()
		RETURNING ` + moveCols
	row := s.pool.QueryRow(ctx, q, m.TaskID, m.Kind, status, m.TargetNode, m.Reason, outcome)
	return scanMove(row)
}

// UpdateMoveStatus 更新 Move 状态（pending → active → done/abandoned）。
func (s *Store) UpdateMoveStatus(ctx context.Context, moveID string, status MoveStatus) error {
	const q = `UPDATE wm_move SET status = $1, updated_at = now() WHERE id = $2`
	_, err := s.pool.Exec(ctx, q, status, moveID)
	if err != nil {
		return fmt.Errorf("worldmodel: UpdateMoveStatus(%s, %s): %w", moveID, status, err)
	}
	return nil
}

// GetMove 按 UUID 主键查单个 Move。
func (s *Store) GetMove(ctx context.Context, moveID string) (Move, error) {
	const q = `SELECT ` + moveCols + ` FROM wm_move WHERE id = $1`
	row := s.pool.QueryRow(ctx, q, moveID)
	m, err := scanMove(row)
	if err != nil {
		return Move{}, fmt.Errorf("worldmodel: GetMove(%s): %w", moveID, err)
	}
	return m, nil
}

// ListMoves 列出某次扫描的所有 Move（按创建时间正序）。
func (s *Store) ListMoves(ctx context.Context, taskID string) ([]Move, error) {
	const q = `SELECT ` + moveCols + ` FROM wm_move WHERE task_id = $1 ORDER BY created_at ASC`
	rows, err := s.pool.Query(ctx, q, taskID)
	if err != nil {
		return nil, fmt.Errorf("worldmodel: ListMoves(%s): %w", taskID, err)
	}
	defer rows.Close()

	var moves []Move
	for rows.Next() {
		m, err := scanMove(rows)
		if err != nil {
			return nil, err
		}
		moves = append(moves, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("worldmodel: ListMoves 迭代失败: %w", err)
	}
	return moves, nil
}

// scanMove 从 pgx.Row 或 pgx.Rows 扫描一个 Move。
func scanMove(row interface {
	Scan(...interface{}) error
}) (Move, error) {
	var m Move
	if err := row.Scan(
		&m.ID, &m.TaskID, &m.Kind, &m.Status, &m.TargetNode,
		&m.Reason, &m.Outcome, &m.CreatedAt, &m.UpdatedAt,
	); err != nil {
		if err == pgx.ErrNoRows {
			return Move{}, err
		}
		return Move{}, fmt.Errorf("worldmodel: 扫描 Move 失败: %w", err)
	}
	return m, nil
}
