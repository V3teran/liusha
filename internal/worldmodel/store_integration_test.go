//go:build integration

package worldmodel_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/db"
	"github.com/V3teran/liusha/internal/worldmodel"
)

// 用 dev DB 验证世界模型运行时行为（不只是编译）：
//   - UpsertNode 幂等 + attrs 合并 + confidence 单向晋升
//   - Lead→Finding 晋升链：RecordVerification → confirmed 节点 → enables 边
//   - ListNodes/ListEdges 回读
// 需 LIUSHA_POSTGRES_DSN；未设则 skip。
//
//	LIUSHA_POSTGRES_DSN='postgres://liusha:liusha@localhost:5432/liusha?sslmode=disable' \
//	  go test -tags integration ./internal/worldmodel/
func TestWorldModel_PromotionFlow(t *testing.T) {
	dsn := os.Getenv("LIUSHA_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("LIUSHA_POSTGRES_DSN 未设，跳过 worldmodel 集成测试")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := db.NewPgPool(ctx, dsn, 5, 1, 5, 0)
	if err != nil {
		t.Fatalf("连库: %v", err)
	}
	defer pool.Close()
	store := worldmodel.NewStore(pool)

	const taskID = "wm-itest-promotion"
	// wm_node/wm_edge/wm_verification 均无父 FK，按 task_id 直接清理。
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM wm_node WHERE task_id=$1`, taskID)         // 边 CASCADE 随节点删
		_, _ = pool.Exec(ctx, `DELETE FROM wm_verification WHERE task_id=$1`, taskID) // 无 FK，单独删
	}()

	// 1) 先落一个 assumed 的 asset（探测发现的攻击面）。
	asset, err := store.UpsertNode(ctx, worldmodel.Node{
		TaskID: taskID,
		Kind:   worldmodel.KindAsset,
		Ref:    worldmodel.TargetRef{Domain: "web", RefKind: "endpoint", Locator: "https://t.local/login"},
		Attrs:  json.RawMessage(`{"service":"http","discovered_via":"crawl"}`),
	})
	if err != nil {
		t.Fatalf("落 asset: %v", err)
	}
	if asset.Confidence != worldmodel.ConfAssumed {
		t.Errorf("新节点默认应 assumed, got %s", asset.Confidence)
	}
	if asset.Seq == 0 {
		t.Error("Seq 应由 bigserial 分配（非 0）")
	}

	// 2) 幂等 upsert：同三元组再写，attrs 合并、行不新增、Seq 不变。
	asset2, err := store.UpsertNode(ctx, worldmodel.Node{
		TaskID: taskID,
		Kind:   worldmodel.KindAsset,
		Ref:    asset.Ref,
		Attrs:  json.RawMessage(`{"tech":"nginx"}`),
	})
	if err != nil {
		t.Fatalf("幂等 upsert: %v", err)
	}
	if asset2.ID != asset.ID || asset2.Seq != asset.Seq {
		t.Errorf("幂等 upsert 应命中同一行: id %s/%s seq %d/%d", asset.ID, asset2.ID, asset.Seq, asset2.Seq)
	}
	var merged map[string]any
	if err := json.Unmarshal(asset2.Attrs, &merged); err != nil {
		t.Fatalf("解析合并 attrs: %v", err)
	}
	if merged["service"] != "http" || merged["tech"] != "nginx" {
		t.Errorf("attrs 应合并保留旧键+新键, got %v", merged)
	}

	// 3) Verifier 复检通过 → 记录取证。
	verID, err := store.RecordVerification(ctx, worldmodel.Verification{
		TaskID:     taskID,
		LeadID:     "lead-sqli-1",
		Primitives: json.RawMessage(`[{"op":"http_request"}]`),
		Outcome:    worldmodel.OutcomeConfirmed,
		Evidence:   json.RawMessage(`{"poc":"' OR 1=1--"}`),
		DurationMs: 1200,
	})
	if err != nil {
		t.Fatalf("记录 verification: %v", err)
	}
	if verID == "" {
		t.Fatal("verification id 不应为空")
	}

	// 4) 晋升：落 confirmed 的 finding 节点，verified_by 指向取证记录。
	finding, err := store.UpsertNode(ctx, worldmodel.Node{
		TaskID:     taskID,
		Kind:       worldmodel.KindFinding,
		Ref:        worldmodel.TargetRef{Domain: "web", RefKind: "endpoint", Locator: "https://t.local/login#sqli"},
		Attrs:      json.RawMessage(`{"severity":"high","taxonomy":["owasp:A03"]}`),
		Confidence: worldmodel.ConfConfirmed,
		VerifiedBy: &verID,
	})
	if err != nil {
		t.Fatalf("落 finding: %v", err)
	}
	if finding.Confidence != worldmodel.ConfConfirmed {
		t.Errorf("finding 应 confirmed, got %s", finding.Confidence)
	}
	if finding.VerifiedBy == nil || *finding.VerifiedBy != verID {
		t.Errorf("finding.VerifiedBy 应指向 %s, got %v", verID, finding.VerifiedBy)
	}

	// 5) confidence 单向：对已 confirmed 的 finding 再写 assumed，不得降级。
	fDown, err := store.UpsertNode(ctx, worldmodel.Node{
		TaskID:     taskID,
		Kind:       worldmodel.KindFinding,
		Ref:        finding.Ref,
		Attrs:      json.RawMessage(`{"note":"re-observed"}`),
		Confidence: worldmodel.ConfAssumed,
	})
	if err != nil {
		t.Fatalf("再写 finding: %v", err)
	}
	if fDown.Confidence != worldmodel.ConfConfirmed {
		t.Errorf("confidence 应单向不降级，仍 confirmed, got %s", fDown.Confidence)
	}

	// 6) 连攻击链边：finding on asset（归属）。
	if err := store.LinkEdge(ctx, worldmodel.Edge{
		TaskID: taskID,
		Rel:    worldmodel.RelOn,
		Src:    finding.ID,
		Dst:    asset.ID,
	}); err != nil {
		t.Fatalf("连边: %v", err)
	}
	// 幂等连边：重复不报错、不重复插。
	if err := store.LinkEdge(ctx, worldmodel.Edge{
		TaskID: taskID, Rel: worldmodel.RelOn, Src: finding.ID, Dst: asset.ID,
	}); err != nil {
		t.Fatalf("幂等连边: %v", err)
	}

	// 7) 回读校验。
	nodes, err := store.ListNodes(ctx, taskID)
	if err != nil {
		t.Fatalf("ListNodes: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("应有 2 个节点(asset+finding), got %d", len(nodes))
	}
	edges, err := store.ListEdges(ctx, taskID)
	if err != nil {
		t.Fatalf("ListEdges: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("幂等后应只有 1 条边, got %d", len(edges))
	}
	if edges[0].Rel != worldmodel.RelOn || edges[0].Src != finding.ID || edges[0].Dst != asset.ID {
		t.Errorf("边内容错: %+v", edges[0])
	}
}
