package main

import (
	"testing"
)

// TestSelectPending_FiltersAndSorts 验证只选未通关题，并按难度升序、同难度按 level 升序排列
// （先拿能拿的分）。
func TestSelectPending_FiltersAndSorts(t *testing.T) {
	in := []challenge{
		{UniqueCode: "hard_l1", Difficulty: "hard", Level: 1, IsCompleted: false},
		{UniqueCode: "done", Difficulty: "easy", Level: 1, IsCompleted: true},
		{UniqueCode: "easy_l2", Difficulty: "easy", Level: 2, IsCompleted: false},
		{UniqueCode: "easy_l1", Difficulty: "easy", Level: 1, IsCompleted: false},
		{UniqueCode: "med_l1", Difficulty: "medium", Level: 1, IsCompleted: false},
	}
	got := selectPending(in)
	want := []string{"easy_l1", "easy_l2", "med_l1", "hard_l1"}
	if len(got) != len(want) {
		t.Fatalf("选出 %d 题，期望 %d：%+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].UniqueCode != w {
			t.Errorf("第 %d 题 = %q，期望 %q", i, got[i].UniqueCode, w)
		}
	}
}

// TestSelectPending_UnknownDifficultyLast 验证未知难度排在已知难度之后（保守后置）。
func TestSelectPending_UnknownDifficultyLast(t *testing.T) {
	in := []challenge{
		{UniqueCode: "weird", Difficulty: "insane", Level: 1},
		{UniqueCode: "easy_l1", Difficulty: "easy", Level: 1},
	}
	got := selectPending(in)
	if got[0].UniqueCode != "easy_l1" || got[1].UniqueCode != "weird" {
		t.Fatalf("顺序错误：%+v", got)
	}
}

// TestSelectPending_Empty 验证全部通关时返回空。
func TestSelectPending_Empty(t *testing.T) {
	in := []challenge{{UniqueCode: "a", IsCompleted: true}}
	if got := selectPending(in); len(got) != 0 {
		t.Fatalf("期望空，得到 %+v", got)
	}
}

// TestFilterOnly 验证只跑指定题：保持难度顺序、忽略空白，报告不存在或已通关的题。
func TestFilterOnly(t *testing.T) {
	pending := []challenge{{UniqueCode: "easy_l1"}, {UniqueCode: "med_l1"}, {UniqueCode: "hard_l1"}}

	kept, missing := filterOnly(pending, splitList(" hard_l1, easy_l1 ,,done "))
	if len(kept) != 2 || kept[0].UniqueCode != "easy_l1" || kept[1].UniqueCode != "hard_l1" {
		t.Fatalf("kept=%+v", kept)
	}
	if len(missing) != 1 || missing[0] != "done" {
		t.Fatalf("missing=%v", missing)
	}

	if kept, missing := filterOnly(pending, nil); len(kept) != 3 || missing != nil {
		t.Fatalf("未指定时应原样返回，kept=%+v missing=%v", kept, missing)
	}
}

// TestLoadConfig_RejectsNonPositiveDurations 验证时长配置非法时在任何外部操作之前报错（时长为 0
// 会让轮询 panic），未设置时取缺省。
func TestLoadConfig_RejectsNonPositiveDurations(t *testing.T) {
	t.Setenv("BENCHMARK_BASE_URL", "http://bench.invalid")
	t.Setenv("BENCHMARK_TOKEN", "tok")
	t.Setenv("LIUSHA_TSB_PASS", "pw")
	cfg, err := loadConfig()
	if err != nil || cfg.pollInterval <= 0 || cfg.concurrency != 3 {
		t.Fatalf("缺省配置应合法: %+v %v", cfg, err)
	}
	for _, kv := range [][2]string{
		{"LIUSHA_TSB_POLL_SECONDS", "0"},
		{"LIUSHA_TSB_TASK_DEADLINE_SECONDS", "-5"},
		{"LIUSHA_TSB_IDLE_SECONDS", "abc"},
	} {
		t.Run(kv[0], func(t *testing.T) {
			t.Setenv(kv[0], kv[1])
			if _, err := loadConfig(); err == nil {
				t.Fatalf("%s=%s 应报错", kv[0], kv[1])
			}
		})
	}
}
