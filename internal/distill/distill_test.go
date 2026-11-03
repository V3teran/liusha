package distill

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
