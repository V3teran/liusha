package worldmodel

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/V3teran/liusha/internal/actor"
	"github.com/V3teran/liusha/internal/ledger"
)

// LedgerAdapter 把 *Store 包装成 ledger.Store 接口，供 ledger.New 使用。
// 翻译层：actor.Landmark ↔ worldmodel.Node（wm_node 表）。
type LedgerAdapter struct{ s *Store }

// AsLedgerStore 返回实现 ledger.Store 的适配器。
func (s *Store) AsLedgerStore() *LedgerAdapter { return &LedgerAdapter{s: s} }

// ── ledger.Store 实现 ────────────────────────────────────────────────────────

func (a *LedgerAdapter) UpsertLandmark(ctx context.Context, l actor.Landmark) (actor.Landmark, error) {
	n, err := a.s.UpsertNode(ctx, landmarkToNode(l))
	if err != nil {
		return actor.Landmark{}, fmt.Errorf("ledger_adapter: UpsertLandmark: %w", err)
	}
	return nodeToLandmark(n), nil
}

func (a *LedgerAdapter) GetLandmark(ctx context.Context, id string) (actor.Landmark, error) {
	n, err := a.s.getNodeByID(ctx, id)
	if err != nil {
		return actor.Landmark{}, fmt.Errorf("ledger_adapter: GetLandmark(%s): %w", id, err)
	}
	return nodeToLandmark(n), nil
}

func (a *LedgerAdapter) ListLandmarks(ctx context.Context, taskID string) ([]actor.Landmark, error) {
	nodes, err := a.s.ListNodes(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("ledger_adapter: ListLandmarks: %w", err)
	}
	out := make([]actor.Landmark, len(nodes))
	for i, n := range nodes {
		out[i] = nodeToLandmark(n)
	}
	return out, nil
}

func (a *LedgerAdapter) ListEdges(ctx context.Context, taskID string) ([]ledger.Edge, error) {
	edges, err := a.s.ListEdges(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("ledger_adapter: ListEdges: %w", err)
	}
	out := make([]ledger.Edge, len(edges))
	for i, e := range edges {
		out[i] = ledger.Edge{Src: e.Src, Dst: e.Dst}
	}
	return out, nil
}

// ── 转换辅助 ─────────────────────────────────────────────────────────────────

// landmarkAttrs 是 wm_node.attrs 中存储的 Landmark 专属字段。
type landmarkAttrs struct {
	Summary         string  `json:"summary,omitempty"`
	Detail          string  `json:"detail,omitempty"`
	ConfidenceScore float64 `json:"confidence_score,omitempty"`
	MoveID          string  `json:"move_id,omitempty"`
}

func landmarkToNode(l actor.Landmark) Node {
	attrs, _ := json.Marshal(landmarkAttrs{
		Summary:         l.Summary,
		Detail:          l.Detail,
		ConfidenceScore: l.Confidence,
		MoveID:          l.MoveID,
	})

	conf := ConfAssumed
	if l.State == actor.LandmarkConfirmed {
		conf = ConfConfirmed
	}

	n := Node{
		TaskID: l.TaskID,
		Kind:   landmarkKindToNodeKind(l.Kind),
		Ref: TargetRef{
			Domain:  l.Ref.Domain,
			RefKind: l.Ref.RefKind,
			Locator: l.Ref.Locator,
		},
		Attrs:      json.RawMessage(attrs),
		Confidence: conf,
		CreatedAt:  l.CreatedAt,
		UpdatedAt:  l.UpdatedAt,
	}
	if l.ID != "" {
		n.ID = l.ID
	}
	return n
}

func nodeToLandmark(n Node) actor.Landmark {
	var a landmarkAttrs
	_ = json.Unmarshal(n.Attrs, &a)

	state := actor.LandmarkHypothesized
	if n.Confidence == ConfConfirmed {
		state = actor.LandmarkConfirmed
	}

	createdAt := n.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}

	return actor.Landmark{
		ID:    n.ID,
		TaskID: n.TaskID,
		Kind:  nodeKindToLandmarkKind(n.Kind),
		State: state,
		Ref: actor.LandmarkRef{
			Domain:  n.Ref.Domain,
			RefKind: n.Ref.RefKind,
			Locator: n.Ref.Locator,
		},
		Summary:    a.Summary,
		Detail:     a.Detail,
		Confidence: a.ConfidenceScore,
		MoveID:     a.MoveID,
		CreatedAt:  createdAt,
		UpdatedAt:  n.UpdatedAt,
	}
}

func landmarkKindToNodeKind(k actor.LandmarkKind) NodeKind {
	switch k {
	case actor.LandmarkTarget, actor.LandmarkGoal:
		return KindTarget
	case actor.LandmarkCredential:
		return KindCredential
	case actor.LandmarkWeakness:
		return KindFinding
	case actor.LandmarkSession:
		return KindAccess
	default: // LandmarkService, LandmarkEndpoint, LandmarkArtifact
		return KindAsset
	}
}

func nodeKindToLandmarkKind(k NodeKind) actor.LandmarkKind {
	switch k {
	case KindTarget:
		return actor.LandmarkTarget
	case KindCredential:
		return actor.LandmarkCredential
	case KindFinding:
		return actor.LandmarkWeakness
	case KindAccess:
		return actor.LandmarkSession
	default:
		return actor.LandmarkEndpoint
	}
}
