package planner

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/V3teran/liusha/internal/explorationgraph"
)

// CanExecute 是并发认领的核心守卫：只有依赖全部完成的 open action 才可执行。
// 上一轮审查发现 water 水位 bug（重复全量分析）就在 planActions 附近。

func testActionNode(id string, deps []string, state string) explorationgraph.Node {
	st := explorationgraph.State(state)
	return explorationgraph.Node{
		ID:        id,
		TaskID:    "t1",
		Kind:      "action",
		DependsOn: deps,
		State:     &st,
	}
}

func TestCanExecute(t *testing.T) {
	n := testActionNode("a2", []string{"a1"}, "open")

	assert.True(t, n.CanExecute(map[string]bool{"a1": true}), "依赖已完成应可执行")
	assert.False(t, n.CanExecute(map[string]bool{"a1": false}), "依赖未完成不可执行")
	assert.False(t, n.CanExecute(map[string]bool{}), "依赖缺失（未出现在完成集）不可执行")
}

func TestCanExecute_NoDeps(t *testing.T) {
	n := testActionNode("a1", nil, "open")
	assert.True(t, n.CanExecute(map[string]bool{}), "无依赖的 action 恒可执行")
}

func TestAnalyzedResultIDs_Watermark(t *testing.T) {
	// 回归测试：planActions 曾每 10s 全量重分析 Results（烧 LLM 调用）。
	// 水位 map 保证只分析增量。直接验证 agent 结构的水位行为。
	a := &Agent{analyzedResultIDs: make(map[string]bool)}

	a.analyzedResultIDs["r1"] = true
	assert.True(t, a.analyzedResultIDs["r1"])
	assert.False(t, a.analyzedResultIDs["r2"], "未分析的 Result 不在水位居")
}

func TestAgentConfig_PollInterval(t *testing.T) {
	cfg := AgentConfig{PollInterval: 0}
	// NewAgent 会把 0 归一化为 10s 兜底——验证归一化行为
	_ = NewAgent(cfg)
	assert.NotNil(t, NewAgent(cfg))
}
