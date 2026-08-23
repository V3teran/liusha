// Package ledger — Landmark CRUD + Frontier/TopK 查询。
//
// 薄包装 worldmodel.Store，提供 actor 层视角的 Landmark 操作接口。
// 幂等键：(task_id, kind, ref.Key())
package ledger

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/V3teran/liusha/internal/actor"
)

// Store 是 ledger 依赖的持久化子集（*worldmodel.Store 或测试替身实现）。
type Store interface {
	UpsertLandmark(ctx context.Context, l actor.Landmark) (actor.Landmark, error)
	GetLandmark(ctx context.Context, id string) (actor.Landmark, error)
	ListLandmarks(ctx context.Context, taskID string) ([]actor.Landmark, error)
	ListEdges(ctx context.Context, taskID string) ([]Edge, error)
}

// Edge 是 Landmark 之间的有向边（用于 Frontier 计算）。
type Edge struct {
	Src string // Landmark.ID
	Dst string // Landmark.ID
}

// Ledger 是 Actor 层的世界模型访问口。
type Ledger struct {
	store Store
}

// New 构造 Ledger。
func New(store Store) *Ledger {
	return &Ledger{store: store}
}

// Write 幂等写入一个 Landmark（UPSERT）。
func (l *Ledger) Write(ctx context.Context, lm actor.Landmark) (actor.Landmark, error) {
	if lm.TaskID == "" {
		return actor.Landmark{}, fmt.Errorf("ledger: Landmark.TaskID 必填")
	}
	if lm.CreatedAt.IsZero() {
		lm.CreatedAt = time.Now()
	}
	lm.UpdatedAt = time.Now()
	return l.store.UpsertLandmark(ctx, lm)
}

// Get 按 ID 拉取 Landmark。
func (l *Ledger) Get(ctx context.Context, id string) (actor.Landmark, error) {
	return l.store.GetLandmark(ctx, id)
}

// Frontier 返回 confirmed 且无出边的 Landmark（攻击前沿）。
// 通常 <20 个，注入 PlanReq.Frontier。
func (l *Ledger) Frontier(ctx context.Context, taskID string) ([]actor.Landmark, error) {
	landmarks, err := l.store.ListLandmarks(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("ledger: frontier list: %w", err)
	}
	edges, err := l.store.ListEdges(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("ledger: frontier edges: %w", err)
	}

	// 有出边的节点
	hasOutEdge := make(map[string]bool, len(edges))
	for _, e := range edges {
		hasOutEdge[e.Src] = true
	}

	var frontier []actor.Landmark
	for _, lm := range landmarks {
		if lm.State == actor.LandmarkConfirmed && !hasOutEdge[lm.ID] {
			frontier = append(frontier, lm)
		}
	}
	return frontier, nil
}

// TopK 按与 ref 的相关性返回 Top-K Landmark Summary。
// 相关性启发式：同 Domain 优先，Key 共享前缀次之，其余按 Confidence 降序。
func (l *Ledger) TopK(ctx context.Context, taskID string, ref actor.LandmarkRef, k int) ([]actor.Landmark, error) {
	landmarks, err := l.store.ListLandmarks(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("ledger: topk list: %w", err)
	}

	type scored struct {
		lm    actor.Landmark
		score float64
	}
	var candidates []scored
	for _, lm := range landmarks {
		if lm.State == actor.LandmarkRefuted {
			continue // 被证伪的不进上下文
		}
		s := scoreRelevance(lm.Ref, ref, lm.Confidence)
		candidates = append(candidates, scored{lm: lm, score: s})
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].score > candidates[j].score
	})

	if k <= 0 {
		k = 30
	}
	if len(candidates) > k {
		candidates = candidates[:k]
	}

	out := make([]actor.Landmark, len(candidates))
	for i, c := range candidates {
		lm := c.lm
		lm.Detail = "" // TopK 只返回 Summary，Detail 由 read_landmark 按需拉
		out[i] = lm
	}
	return out, nil
}

func scoreRelevance(a, ref actor.LandmarkRef, confidence float64) float64 {
	var s float64
	if a.Domain == ref.Domain {
		s += 2.0
	}
	if a.RefKind == ref.RefKind {
		s += 1.0
	}
	if a.Locator != "" && ref.Locator != "" &&
		strings.HasPrefix(a.Locator, commonPrefix(a.Locator, ref.Locator)) {
		s += 0.5
	}
	s += confidence
	return s
}

func commonPrefix(a, b string) string {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return a[:i]
		}
	}
	return a[:n]
}

// ────────────────────────────────────────────────────────────────
//  Move Provenance（新增：追踪 Move 执行溯源）
// ────────────────────────────────────────────────────────────────

// MoveStore 是 Move CRUD 的持久化接口（由 worldmodel.Store 实现）。
type MoveStore interface {
	UpsertMove(ctx context.Context, m Move) (Move, error)
	UpdateMoveStatus(ctx context.Context, moveID string, status MoveStatus) error
	GetMove(ctx context.Context, moveID string) (Move, error)
	ListMoves(ctx context.Context, taskID string) ([]Move, error)
	LinkEdge(ctx context.Context, e MoveEdge) error
}

// Move 是 Ledger 层对 worldmodel.Move 的投影（避免循环依赖）。
type Move struct {
	ID         string
	TaskID     string
	Kind       string // enumerate/probe/exploit/escalate/persist
	Status     MoveStatus
	TargetNode string
	Reason     string
	Outcome    map[string]interface{}
}

// MoveStatus 是 Move 执行状态。
type MoveStatus string

const (
	MoveStatusPending   MoveStatus = "pending"
	MoveStatusActive    MoveStatus = "active"
	MoveStatusDone      MoveStatus = "done"
	MoveStatusAbandoned MoveStatus = "abandoned"
)

// MoveEdge 是 Move Provenance 边（spawns/produces）。
type MoveEdge struct {
	TaskID string
	Rel    string // spawns/produces
	Src    string // Node.ID 或 Move.ID
	Dst    string // Move.ID 或 Node.ID
}

// RecordMoveStart 记录 Move 开始执行：写入 wm_move + 创建 spawns 边（TargetNode → Move）。
func (l *Ledger) RecordMoveStart(ctx context.Context, taskID string, kind string, targetNodeID string, reason string) (string, error) {
	if taskID == "" || kind == "" || targetNodeID == "" {
		return "", fmt.Errorf("ledger: RecordMoveStart 参数不全")
	}

	// 1. 写入 wm_move 表
	m, err := l.store.(MoveStore).UpsertMove(ctx, Move{
		TaskID:     taskID,
		Kind:       kind,
		Status:     MoveStatusActive,
		TargetNode: targetNodeID,
		Reason:     reason,
		Outcome:    map[string]interface{}{},
	})
	if err != nil {
		return "", fmt.Errorf("ledger: RecordMoveStart upsert: %w", err)
	}

	// 2. 创建 spawns 边：TargetNode → Move
	if err := l.store.(MoveStore).LinkEdge(ctx, MoveEdge{
		TaskID: taskID,
		Rel:    "spawns",
		Src:    targetNodeID,
		Dst:    m.ID,
	}); err != nil {
		return "", fmt.Errorf("ledger: RecordMoveStart link spawns: %w", err)
	}

	return m.ID, nil
}

// RecordMoveComplete 记录 Move 完成：更新状态 + 写 outcome + 创建 produces 边（Move → 产出节点）。
func (l *Ledger) RecordMoveComplete(ctx context.Context, moveID string, outcome map[string]interface{}, producedNodeIDs []string) error {
	if moveID == "" {
		return fmt.Errorf("ledger: RecordMoveComplete moveID 必填")
	}

	// 1. 更新 Move 状态为 done
	if err := l.store.(MoveStore).UpdateMoveStatus(ctx, moveID, MoveStatusDone); err != nil {
		return fmt.Errorf("ledger: RecordMoveComplete status: %w", err)
	}

	// 2. 获取 Move 的 TaskID（用于创建边）
	m, err := l.store.(MoveStore).GetMove(ctx, moveID)
	if err != nil {
		return fmt.Errorf("ledger: RecordMoveComplete get move: %w", err)
	}

	// 3. 为每个产出节点创建 produces 边：Move → Node
	for _, nodeID := range producedNodeIDs {
		if err := l.store.(MoveStore).LinkEdge(ctx, MoveEdge{
			TaskID: m.TaskID,
			Rel:    "produces",
			Src:    moveID,
			Dst:    nodeID,
		}); err != nil {
			return fmt.Errorf("ledger: RecordMoveComplete link produces: %w", err)
		}
	}

	return nil
}
