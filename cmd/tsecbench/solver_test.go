package main

import (
	"fmt"
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
	if strings.Contains(goal, "hypothesis") {
		t.Errorf("回报规约属于 ctf 场景，不应写进目标：\n%s", goal)
	}
	// 题型不限于 Web；授权范围写明只限平台给出的地址。
	if strings.Contains(goal, "Web") || !strings.Contains(goal, "测试范围仅限上述地址") {
		t.Errorf("目标应不限题型并写明授权范围：\n%s", goal)
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

// TestBuildGoal_FlagCountWritten 验证平台给出数量就写进目标（含 1）：数量是收敛依据，
// 普通题拿到真 flag 即 1/1 收敛；未知（0）才不写。
func TestBuildGoal_FlagCountWritten(t *testing.T) {
	for _, n := range []int{1, 3} {
		goal := buildGoal(challenge{UniqueCode: "x", FlagCount: n}, []string{"10.0.1.2:8080"})
		want := fmt.Sprintf("本题共有 %d 个 flag", n)
		if !strings.Contains(goal, want) {
			t.Fatalf("FlagCount=%d 的 goal 应含 %q：%s", n, want, goal)
		}
	}
	if goal := buildGoal(challenge{UniqueCode: "x"}, []string{"10.0.1.2:8080"}); strings.Contains(goal, "个 flag，全部捕获") {
		t.Fatalf("未知数量不应写收敛口径：%s", goal)
	}
}
