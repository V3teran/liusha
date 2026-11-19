package main

import (
	"strings"
	"testing"
)

// TestBuildGoal_IncludesTargetsAndFlagInstruction 验证 goal 含靶场地址、找 flag 目标与题面，
// 且不注入任何题解/历史答案（闭卷合规）。flag 的回报规约在 ctf 场景里，不在目标里。
func TestBuildGoal_IncludesTargetsAndFlagInstruction(t *testing.T) {
	ch := challenge{
		UniqueCode:  "web_sql_injection_01",
		Description: "通过 SQL 注入获取管理员凭证并读取 flag",
		FlagCount:   2,
	}
	addrs := []string{"10.0.1.5:8080", "10.0.1.5:9090"}
	goal := buildGoal(ch, addrs)

	for _, must := range []string{"10.0.1.5:8080", "10.0.1.5:9090", "flag", ch.Description} {
		if !strings.Contains(goal, must) {
			t.Errorf("goal 应含 %q，实际：\n%s", must, goal)
		}
	}
	if strings.Contains(goal, "entity") {
		t.Errorf("回报规约属于 ctf 场景，不应写进目标：\n%s", goal)
	}
	// 多 flag 题应提示 flag 数量，促使 agent 找全。
	if !strings.Contains(goal, "2") {
		t.Errorf("多 flag 题 goal 应提示 flag 数量，实际：\n%s", goal)
	}
}

// TestBuildGoal_SingleLine 验证 goal 是单行文本：POST /api/tasks 按行拆 brief、每行一个任务，
// 多行 goal 会把一道题拆成多个任务（适配器只跟踪第一个，其余在后台空烧 token）。
// 题目说明自带的换行、回车、制表符也要折叠。
func TestBuildGoal_SingleLine(t *testing.T) {
	ch := challenge{
		UniqueCode:  "web_doc_viewer",
		Description: "内部文档查看器。\r\n提示：\n\t注意路径参数\n\n",
		FlagCount:   2,
	}
	goal := buildGoal(ch, []string{"172.21.0.5:8080"})
	if strings.ContainsAny(goal, "\r\n") {
		t.Fatalf("goal 不应含换行，实际：%q", goal)
	}
	for _, must := range []string{"内部文档查看器。 提示： 注意路径参数", "172.21.0.5:8080"} {
		if !strings.Contains(goal, must) {
			t.Errorf("goal 应含 %q，实际：%q", must, goal)
		}
	}
}

// TestBuildGoal_NoContainerAddr 验证无地址时 goal 仍成立（不崩，给出说明）。
func TestBuildGoal_NoContainerAddr(t *testing.T) {
	goal := buildGoal(challenge{UniqueCode: "x", FlagCount: 1}, nil)
	if strings.TrimSpace(goal) == "" {
		t.Fatal("goal 不应为空")
	}
}
