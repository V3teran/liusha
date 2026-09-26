//go:build integration

package evaluator_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/V3teran/liusha/internal/dbtest"
	"github.com/V3teran/liusha/internal/evaluator"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
)

// stubReplayer 按预设结论回应复现——集成测试只评估"门 + 真 store"咬合，不测真实复现。
type stubReplayer struct{ res evaluator.Result }

func (s stubReplayer) Replay(context.Context, json.RawMessage) (evaluator.Result, error) {
	return s.res, nil
}

// TestEvaluator_PromoteAgainstRealStore 用真 explorationgraph.Store 跑晋升门，证明：
//   - 坐实 → wm_verification 落 confirmed + Result 节点晋升（confidence=verified，SourceID 回指 verification）；
//   - 证伪 → wm_verification 落 refuted 留档，wm_node 不新增。
func TestEvaluator_PromoteAgainstRealStore(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	store := explorationgraph.NewStore(pool)

	const taskID = "verifier-itest"
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM wm_node WHERE task_id=$1`, taskID)
		_, _ = pool.Exec(ctx, `DELETE FROM wm_verification WHERE task_id=$1`, taskID)
	}()

	mkAttempt := func(loc string) evaluator.Attempt {
		return evaluator.Attempt{
			TaskID:     taskID,
			NodeID:     "lead-" + loc,
			Kind:       core.KindResult,
			Primitives: json.RawMessage(`[{"op":"http_request"}]`),
			Content:    json.RawMessage(`{"severity":"high","host":"` + loc + `","summary":"SQLi at ` + loc + `"}`),
			Priority:   "high",
		}
	}

	// 坐实：应晋升 verified 节点，SourceID 指向真实 wm_verification.id。
	confirmed := evaluator.New(store, stubReplayer{res: evaluator.Result{
		Confirmed: true, Evaluation: json.RawMessage(`{"poc":"' OR 1=1--"}`), DurationMs: 88,
	}}, nil)
	node, err := confirmed.Promote(ctx, mkAttempt("t.local"))
	if err != nil {
		t.Fatalf("坐实 Promote: %v", err)
	}
	if node == nil || node.Confidence == nil || *node.Confidence != explorationgraph.ConfidenceVerified {
		t.Fatalf("应晋升 verified 节点, got %+v", node)
	}
	if node.SourceID == "" {
		t.Fatal("SourceID 应指向真实 verification id")
	}

	// 证伪：不进图，但 wm_verification 留 refuted 档。
	refuted := evaluator.New(store, stubReplayer{res: evaluator.Result{
		Confirmed: false, Evaluation: json.RawMessage(`{"reason":"no repro"}`),
	}}, nil)
	rNode, err := refuted.Promote(ctx, mkAttempt("safe.local"))
	if err != nil {
		t.Fatalf("证伪 Promote 不应报错: %v", err)
	}
	if rNode != nil {
		t.Errorf("证伪不应进图, got %+v", rNode)
	}

	// 回读：图里只有 1 个坐实节点（证伪的没进）；verification 有 2 条（含 refuted 留档）。
	nodes, err := store.ListNodesByKind(ctx, taskID, core.KindResult)
	if err != nil {
		t.Fatalf("ListNodesByKind: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("图应只有 1 个坐实节点, got %d", len(nodes))
	}

	var verCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM wm_verification WHERE task_id=$1`, taskID).Scan(&verCount); err != nil {
		t.Fatalf("查 verification 数: %v", err)
	}
	if verCount != 2 {
		t.Errorf("应有 2 条 verification（坐实+证伪留档）, got %d", verCount)
	}
}
