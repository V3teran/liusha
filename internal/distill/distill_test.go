package distill

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
)

// TestAssembleMaterials_BucketPriority 分桶优先：results 全保，预算耗尽后
// 低优桶整体让位且计数留痕——最珍贵的已确认发现永不先丢。
func TestAssembleMaterials_BucketPriority(t *testing.T) {
	results := []string{"已确认发现：R1", "已确认发现：R2"}
	facts := make([]string, 2000) // facts 体量压垮预算（索引编进串内便于断言）
	for i := range facts {
		facts[i] = fmt.Sprintf("已确认事实：F%04d", i) + strings.Repeat("x", 20)
	}
	refuted := []string{"已证伪假设：H1"}

	got := assembleMaterials(results, facts, refuted)
	joined := strings.Join(got, "\n")

	total := 0
	for _, m := range got {
		total += len(m)
	}
	assert.LessOrEqual(t, total, distillMaterialsMaxRunes*2, "总量应有界（含标记行容差）")
	// results 全保（价值最高，永不先丢）
	assert.Contains(t, joined, "R1")
	assert.Contains(t, joined, "R2")
	// facts 桶内保新：最新在场、被让位的最旧不在
	assert.Contains(t, joined, "F1999")
	assert.NotContains(t, joined, "F0500")
	// refuted 整桶让位，未纳入计数进标记
	assert.NotContains(t, joined, "H1")
	assert.Contains(t, joined, "另有")
}

// TestAssembleMaterials_AllFit 预算内全量保留、无标记行、桶序正确。
func TestAssembleMaterials_AllFit(t *testing.T) {
	got := assembleMaterials(
		[]string{"R1"},
		[]string{"F1", "F2"},
		[]string{"H1"},
	)
	require.Len(t, got, 4)
	assert.Equal(t, "R1", got[0], "results 桶在前")
	assert.Equal(t, "F1", got[1], "桶内时间正序")
	assert.Equal(t, "F2", got[2])
	assert.Equal(t, "H1", got[3])
	assert.False(t, strings.HasPrefix(got[3], "…（"), "预算内不得出现截断标记")
}

// TestAssembleMaterials_Empty 全空入参返回空（调用方据此跳过蒸馏）。
func TestAssembleMaterials_Empty(t *testing.T) {
	assert.Empty(t, assembleMaterials(nil, nil, nil))
	assert.Empty(t, assembleMaterials())
}

// 材料覆盖事实与全部已证伪假设（无论因何出圈，都带原因）；待验证与已坐实的假设不是材料
// （坐实的经 result 进材料）。
func TestCollect_FactsAndEveryRefutedHypothesis(t *testing.T) {
	graph := explorationgraph.NewMemoryStore()
	status := func(v explorationgraph.VerificationStatus) *explorationgraph.VerificationStatus { return &v }
	obs := func(id, statement string, v *explorationgraph.VerificationStatus, reason string) explorationgraph.Node {
		n := explorationgraph.Node{ID: id, TaskID: "t", Kind: core.KindObservation,
			Content: json.RawMessage(`{"statement":"` + statement + `"}`), VerificationStatus: v}
		if reason != "" {
			n.Metadata = json.RawMessage(`{"refute_reason":"` + reason + `"}`)
		}
		return n
	}
	for _, n := range []explorationgraph.Node{
		obs("f", "F", nil, ""),
		obs("p", "P", status(explorationgraph.VerificationPending), ""),
		obs("c", "C", status(explorationgraph.VerificationConfirmed), ""),
		obs("r1", "R1", status(explorationgraph.VerificationRefuted), "claim_invalid"),
		obs("r2", "R2", status(explorationgraph.VerificationRefuted), "judge_unavailable"),
	} {
		if _, err := graph.CreateNode(context.Background(), n); err != nil {
			t.Fatal(err)
		}
	}
	s := &Service{graph: graph, logger: zerolog.Nop()}
	got := s.collect(context.Background(), "t")
	want := []string{"已确认事实：F", "已证伪假设：R1（原因：claim_invalid）", "已证伪假设：R2（原因：judge_unavailable）"}
	if !slices.Equal(slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values(want))) {
		t.Fatalf("材料 = %q，期望 %q", got, want)
	}
}
