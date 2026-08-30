//go:build integration

package verifier_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/db"
	"github.com/V3teran/liusha/internal/verifier"
	"github.com/V3teran/liusha/internal/worldmodel"
)

// stubReplayer 按预设结论回应复现——集成测试只验证"门 + 真 store"咬合，不测真实复现。
type stubReplayer struct{ res verifier.Result }

func (s stubReplayer) Replay(context.Context, json.RawMessage) (verifier.Result, error) {
	return s.res, nil
}

// Verifier 承接 L3 的关键验证：用真 worldmodel.Store 跑晋升门，证明
//   - 坐实 → wm_verification 落 confirmed + wm_node 晋升 confirmed + verified_by 回指闭环；
//   - 证伪 → wm_verification 落 refuted 留档，wm_node 不新增。
// 需 LIUSHA_POSTGRES_DSN；未设则 skip。
func TestVerifier_PromoteAgainstRealStore(t *testing.T) {
	dsn := os.Getenv("LIUSHA_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("LIUSHA_POSTGRES_DSN 未设，跳过 verifier 集成测试")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := db.NewPgPool(ctx, dsn, 5, 1, 5, 0)
	if err != nil {
		t.Fatalf("连库: %v", err)
	}
	defer pool.Close()
	store := worldmodel.NewStore(pool)

	const taskID = "verifier-itest"
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM wm_node WHERE task_id=$1`, taskID)
		_, _ = pool.Exec(ctx, `DELETE FROM wm_verification WHERE task_id=$1`, taskID)
	}()

	mkAttempt := func(loc string) verifier.Attempt {
		return verifier.Attempt{
			TaskID:     taskID,
			LeadID:     "lead-" + loc,
			Kind:       worldmodel.KindFinding,
			Target:     worldmodel.TargetRef{Domain: "web", RefKind: "host", Locator: loc},
			Primitives: json.RawMessage(`[{"op":"http_request"}]`),
			Attrs:      json.RawMessage(`{"severity":"high","taxonomy":["owasp:A03"]}`),
		}
	}

	// 坐实：应晋升 confirmed 节点，verified_by 指向真实 wm_verification.id。
	confirmed := verifier.New(store, stubReplayer{res: verifier.Result{
		Confirmed: true, Evidence: json.RawMessage(`{"poc":"' OR 1=1--"}`), DurationMs: 88,
	}})
	node, err := confirmed.Promote(ctx, mkAttempt("t.local"))
	if err != nil {
		t.Fatalf("坐实 Promote: %v", err)
	}
	if node == nil || node.Confidence != worldmodel.ConfConfirmed {
		t.Fatalf("应晋升 confirmed 节点, got %+v", node)
	}
	if node.VerifiedBy == nil || *node.VerifiedBy == "" {
		t.Fatal("VerifiedBy 应指向真实 verification id")
	}

	// 证伪：不进图，但 wm_verification 留 refuted 档。
	refuted := verifier.New(store, stubReplayer{res: verifier.Result{
		Confirmed: false, Evidence: json.RawMessage(`{"reason":"no repro"}`),
	}})
	rNode, err := refuted.Promote(ctx, mkAttempt("safe.local"))
	if err != nil {
		t.Fatalf("证伪 Promote 不应报错: %v", err)
	}
	if rNode != nil {
		t.Errorf("证伪不应进图, got %+v", rNode)
	}

	// 回读：图里只有 1 个坐实节点（证伪的没进）；verification 有 2 条（含 refuted 留档）。
	nodes, err := store.ListNodes(ctx, taskID)
	if err != nil {
		t.Fatalf("ListNodes: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("图应只有 1 个坐实节点, got %d", len(nodes))
	}
	if nodes[0].Ref.Locator != "t.local" {
		t.Errorf("坐实的应是 t.local, got %s", nodes[0].Ref.Locator)
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
