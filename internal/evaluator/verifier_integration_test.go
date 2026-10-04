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

// verdictJudge 固定裁决（集成测试只测 store 咬合，语义裁决由单测覆盖）。
type verdictJudge struct{ verdict string }

func (j verdictJudge) Judge(context.Context, string, json.RawMessage, json.RawMessage, evaluator.ReplayFunc) (string, string, error) {
	return j.verdict, "集成测试固定裁决", nil
}

// TestEvaluator_PromoteAgainstRealStore 用真 explorationgraph.Store 跑晋升门，证明：
//   - 坐实 → exploration_verification 落 confirmed + Result 节点晋升（confidence=verified，SourceID 回指 verification）；
//   - 证伪 → exploration_verification 落 refuted 留档，exploration_node 不新增。
func TestEvaluator_PromoteAgainstRealStore(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	store := explorationgraph.NewStore(pool)

	const taskID = "verifier-itest"
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM exploration_node WHERE task_id=$1`, taskID)
		_, _ = pool.Exec(ctx, `DELETE FROM exploration_verification WHERE task_id=$1`, taskID)
	}()

	mkAttempt := func(loc string) evaluator.Attempt {
		return evaluator.Attempt{
			TaskID:     taskID,
			NodeID:     "lead-" + loc,
			Kind:       core.KindResult,
			Primitives: json.RawMessage(`{"request":{"method":"GET","url":"http://` + loc + `/api?id=1","headers":{},"body":""},"assert":{"body_contains":["leaked"]}}`),
			Content:    json.RawMessage(`{"severity":"high","host":"` + loc + `","summary":"SQLi at ` + loc + `"}`),
			Priority:   "high",
		}
	}

	// 坐实：应晋升 verified 节点，SourceID 指向真实 exploration_verification.id。
	confirmed := evaluator.New(store, stubReplayer{res: evaluator.Result{
		Evaluation: json.RawMessage(`{"poc":"' OR 1=1--"}`), DurationMs: 88,
	}}, nil).WithJudge(verdictJudge{verdict: evaluator.VerdictConfirmed})
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

	// 证伪：不进图，但 exploration_verification 留 refuted 档。
	refuted := evaluator.New(store, stubReplayer{res: evaluator.Result{
		Evaluation: json.RawMessage(`{"reason":"no repro"}`),
	}}, nil).WithJudge(verdictJudge{verdict: evaluator.VerdictRefuted})
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
		`SELECT count(*) FROM exploration_verification WHERE task_id=$1`, taskID).Scan(&verCount); err != nil {
		t.Fatalf("查 verification 数: %v", err)
	}
	if verCount != 2 {
		t.Errorf("应有 2 条 verification（坐实+证伪留档）, got %d", verCount)
	}
}

// replayJudgeITest 裁决前自主复放一次（模拟 RouterJudge 的 replay_for_verification）。
type replayJudgeITest struct{ verdict string }

func (j replayJudgeITest) Judge(_ context.Context, _ string, _, _ json.RawMessage, replay evaluator.ReplayFunc) (string, string, error) {
	_, _ = replay(context.Background())
	return j.verdict, "集成测试复放后裁决", nil
}

// TestEvaluator_WritesOnlyResultNodes 真 store 下锁定图的写权限不变式：
// evaluator 对图的唯一写入是晋升 result 节点——confirmed 恰 1 个 result、零 observation；
// refuted 零节点。复放审计全在 exploration_verification.evidence.replays。
func TestEvaluator_WritesOnlyResultNodes(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	store := explorationgraph.NewStore(pool)

	const taskID = "verifier-itest-invariant"
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM exploration_node WHERE task_id=$1`, taskID)
		_, _ = pool.Exec(ctx, `DELETE FROM exploration_verification WHERE task_id=$1`, taskID)
	}()

	promote := func(verdict string) (*explorationgraph.Node, error) {
		v := evaluator.New(store, stubReplayer{res: evaluator.Result{
			Evaluation: json.RawMessage(`{"url":"http://t.local/x"}`), DurationMs: 5,
		}}, nil).WithJudge(replayJudgeITest{verdict: verdict})
		return v.Promote(ctx, evaluator.Attempt{
			TaskID: taskID, NodeID: "hyp-1", Kind: core.KindResult,
			Primitives: json.RawMessage(`{"domain":"web","recipe":{"request":{"method":"GET","url":"http://t.local/x","headers":{},"body":""}},"assert":{"body_contains":["y"]}}`),
			Content:    json.RawMessage(`{"summary":"s","severity":"high"}`),
		})
	}

	// 坐实：图中新增恰好 1 个节点，且是 result。
	node, err := promote(evaluator.VerdictConfirmed)
	if err != nil {
		t.Fatalf("坐实 Promote: %v", err)
	}
	if node == nil || node.ID == "" {
		t.Fatal("坐实应返回晋升节点")
	}
	var kinds []string
	if err := pool.QueryRow(ctx,
		`SELECT array_agg(kind ORDER BY created_at) FROM exploration_node WHERE task_id=$1`, taskID).Scan(&kinds); err != nil {
		t.Fatalf("查节点: %v", err)
	}
	if len(kinds) != 1 || kinds[0] != "result" {
		t.Errorf("evaluator 应只写 1 个 result 节点（不得写 observation）, got %v", kinds)
	}

	// 审计链：2 条 verification（坐实+证伪），各含 2 次重放（预跑+裁决复放）。
	if _, err := promote(evaluator.VerdictRefuted); err != nil {
		t.Fatalf("证伪 Promote 不应报错: %v", err)
	}
	var refutedNodes int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM exploration_node WHERE task_id=$1`, taskID).Scan(&refutedNodes); err != nil {
		t.Fatalf("查节点数: %v", err)
	}
	if refutedNodes != 1 {
		t.Errorf("证伪不应新增节点（应仍为 1）, got %d", refutedNodes)
	}
	var verCount, replayCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*), count(*) FILTER (WHERE jsonb_array_length(evidence->'replays') = 2)
		FROM exploration_verification WHERE task_id=$1`, taskID).Scan(&verCount, &replayCount); err != nil {
		t.Fatalf("查 verification: %v", err)
	}
	if verCount != 2 || replayCount != 2 {
		t.Errorf("应有 2 条 verification 且各含 2 次重放审计, got ver=%d with-2-replays=%d", verCount, replayCount)
	}
}
