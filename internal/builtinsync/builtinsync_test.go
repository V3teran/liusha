package builtinsync

import (
	"slices"
	"testing"
)

func ptr(s string) *string { return &s }

func TestPlan(t *testing.T) {
	v1, v2 := Hash("名", "描述", "正文 v1"), Hash("名", "描述", "正文 v2")
	edited := Hash("名", "描述", "用户改过")
	seeds := []Seed{
		{"new", v2},      // 库里没有
		{"same", v2},     // 未改、已是最新
		{"stale", v2},    // 未改、出厂有新版
		{"edited", v2},   // 改过
		{"migrated", v2}, // 基线未知、内容恰与新版相同
		{"unknown", v2},  // 基线未知、内容不同
		{"shadowed", v2}, // 自建同 code
		{"reverted", v2}, // 改了又改回原样（内容 == seed_hash）
		{"deleted", v2},  // 用户删除了它：不补回、不更新
	}
	rows := []Row{
		{Code: "same", IsBuiltin: true, Hash: v2, SeedHash: ptr(v2)},
		{Code: "stale", IsBuiltin: true, Hash: v1, SeedHash: ptr(v1)},
		{Code: "edited", IsBuiltin: true, Hash: edited, SeedHash: ptr(v1)},
		{Code: "migrated", IsBuiltin: true, Hash: v2},
		{Code: "unknown", IsBuiltin: true, Hash: edited},
		{Code: "shadowed", IsBuiltin: false, Hash: edited},
		{Code: "reverted", IsBuiltin: true, Hash: v1, SeedHash: ptr(v1)},
		{Code: "gone-clean", IsBuiltin: true, Hash: v1, SeedHash: ptr(v1)},
		{Code: "gone-edited", IsBuiltin: true, Hash: edited, SeedHash: ptr(v1)},
		{Code: "mine", IsBuiltin: false, Hash: edited},
		{Code: "deleted", IsBuiltin: true, Deleted: true, Hash: v1, SeedHash: ptr(v1)},
		{Code: "gone-deleted", IsBuiltin: true, Deleted: true, Hash: edited, SeedHash: ptr(v1)},
	}
	want := []Action{
		{"new", OpInsert},
		{"stale", OpUpdate},
		{"migrated", OpBaseline},
		{"reverted", OpUpdate},
		{"gone-clean", OpRetire},
		{"gone-edited", OpDetach},
		{"gone-deleted", OpRetire}, // 已删除的内置项出厂也下线：删除标记随之清掉，不转自建
	}
	if got := Plan(rows, seeds); !slices.Equal(got, want) {
		t.Errorf("Plan =\n %v\nwant\n %v", got, want)
	}
}

func TestPlanIsIdempotentAfterApply(t *testing.T) {
	h := Hash("名", "", "正文")
	seeds := []Seed{{"a", h}}
	rows := []Row{{Code: "a", IsBuiltin: true, Hash: h, SeedHash: ptr(h)}}
	if got := Plan(rows, seeds); len(got) != 0 {
		t.Errorf("已同步的表不应再有动作: %v", got)
	}
}

// 迁移 SQL 用同一算式计算哈希（见 sep 的注释）：这里钉住算式，改动会让迁移来的行全部误判。
func TestHashIsStable(t *testing.T) {
	if got, want := Hash("a", "b", "c"), "e0dee694aed26d323b0d8b861c343f2554c1aefceef539ae3dae5159f0b5bb28"; got != want {
		t.Fatalf("Hash = %q, want %q", got, want)
	}
	if Hash("a", "b", "c") == Hash("a", "bc", "") {
		t.Error("字段边界必须参与哈希")
	}
}

// 摘要覆盖同步会写进表的全部字段：只改附加列（如技能分类）也要换缓存键。
func TestDigestCoversExtra(t *testing.T) {
	a := []SeedRow{{Code: "x", Name: "n", Body: "b", Extra: []any{"tool"}}}
	b := []SeedRow{{Code: "x", Name: "n", Body: "b", Extra: []any{"vuln"}}}
	if Digest(a) == Digest(b) {
		t.Fatal("附加列不同，摘要应不同")
	}
	if Digest(a) != Digest([]SeedRow{{Code: "x", Name: "n", Body: "b", Extra: []any{"tool"}}}) {
		t.Fatal("相同内容摘要应稳定")
	}
}
