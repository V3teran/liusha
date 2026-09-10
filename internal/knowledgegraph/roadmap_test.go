package knowledgegraph

import (
	"context"
	"testing"

	"github.com/V3teran/liusha/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRoadmapBasicOperations 测试 Roadmap 的基本操作
func TestRoadmapBasicOperations(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	cfg := config.MustLoad()
	store := NewStore(cfg.DatabasePool)

	taskID := "test_roadmap_" + t.Name()

	// 1. 生成初始 Roadmap
	steps := []RoadmapStep{
		{TaskID: taskID, Step: 1.0, Objective: "端口扫描和服务识别", Status: StepTodo},
		{TaskID: taskID, Step: 2.0, Objective: "Web 应用指纹识别", Status: StepTodo, DependsOn: []float64{1.0}},
		{TaskID: taskID, Step: 3.0, Objective: "测试 SQL 注入", Status: StepTodo, DependsOn: []float64{2.0}},
	}

	err := store.SaveRoadmap(ctx, taskID, steps)
	require.NoError(t, err)

	// 2. 加载 Roadmap
	loaded, err := store.LoadRoadmap(ctx, taskID)
	require.NoError(t, err)
	assert.Equal(t, 3, len(loaded))
	assert.Equal(t, "端口扫描和服务识别", loaded[0].Objective)

	// 3. 更新步骤状态
	err = store.UpdateStepStatus(ctx, taskID, 1.0, StepActive)
	require.NoError(t, err)

	// 4. 验证状态更新
	loaded, _ = store.LoadRoadmap(ctx, taskID)
	assert.Equal(t, StepActive, loaded[0].Status)

	// 5. 获取下一个可执行的步骤（Step 1 active，Step 2 依赖 Step 1，不可执行）
	next, err := store.GetNextExecutableStep(ctx, taskID)
	require.NoError(t, err)
	assert.Nil(t, next) // Step 1 未完成，Step 2 阻塞

	// 6. 标记 Step 1 完成
	err = store.UpdateStepStatus(ctx, taskID, 1.0, StepComplete)
	require.NoError(t, err)

	// 7. 现在 Step 2 可执行了
	next, err = store.GetNextExecutableStep(ctx, taskID)
	require.NoError(t, err)
	assert.NotNil(t, next)
	assert.Equal(t, 2.0, next.Step)
	assert.Equal(t, "Web 应用指纹识别", next.Objective)
}

// TestRoadmapDynamicInsertion 测试动态插入步骤
func TestRoadmapDynamicInsertion(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	cfg := config.MustLoad()
	store := NewStore(cfg.DatabasePool)

	taskID := "test_roadmap_insert_" + t.Name()

	// 初始 Roadmap
	steps := []RoadmapStep{
		{TaskID: taskID, Step: 1.0, Objective: "信息收集", Status: StepComplete},
		{TaskID: taskID, Step: 2.0, Objective: "漏洞利用", Status: StepTodo, DependsOn: []float64{1.0}},
	}

	err := store.SaveRoadmap(ctx, taskID, steps)
	require.NoError(t, err)

	// 发现需要在 1.0 和 2.0 之间插入新步骤
	// 动态调整：插入 Step 1.5
	updatedSteps := []RoadmapStep{
		{TaskID: taskID, Step: 1.0, Objective: "信息收集", Status: StepComplete},
		{TaskID: taskID, Step: 1.5, Objective: "漏洞验证", Status: StepTodo, DependsOn: []float64{1.0}},
		{TaskID: taskID, Step: 2.0, Objective: "漏洞利用", Status: StepTodo, DependsOn: []float64{1.5}},
	}

	err = store.SaveRoadmap(ctx, taskID, updatedSteps)
	require.NoError(t, err)

	// 验证插入成功
	loaded, err := store.LoadRoadmap(ctx, taskID)
	require.NoError(t, err)
	assert.Equal(t, 3, len(loaded))
	assert.Equal(t, 1.5, loaded[1].Step)
	assert.Equal(t, "漏洞验证", loaded[1].Objective)
}

// TestRoadmapSummary 测试 Roadmap 摘要
func TestRoadmapSummary(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	cfg := config.MustLoad()
	store := NewStore(cfg.DatabasePool)

	taskID := "test_roadmap_summary_" + t.Name()

	steps := []RoadmapStep{
		{TaskID: taskID, Step: 1.0, Objective: "Step 1", Status: StepComplete},
		{TaskID: taskID, Step: 2.0, Objective: "Step 2", Status: StepActive},
		{TaskID: taskID, Step: 3.0, Objective: "Step 3", Status: StepTodo},
		{TaskID: taskID, Step: 4.0, Objective: "Step 4", Status: StepSkipped},
	}

	err := store.SaveRoadmap(ctx, taskID, steps)
	require.NoError(t, err)

	summary, err := store.GetRoadmapSummary(ctx, taskID)
	require.NoError(t, err)

	assert.Equal(t, 4, summary.TotalSteps)
	assert.Equal(t, 1, summary.CompleteSteps)
	assert.Equal(t, 1, summary.ActiveSteps)
	assert.Equal(t, 1, summary.TodoSteps)
	assert.Equal(t, 1, summary.SkippedSteps)
	assert.Equal(t, 0.25, summary.CompletionRate) // 1/4 = 0.25
}

// TestRoadmapActionAssociation 测试 Roadmap 和 Action 的关联
func TestRoadmapActionAssociation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	cfg := config.MustLoad()
	store := NewStore(cfg.DatabasePool)

	taskID := "test_roadmap_action_" + t.Name()

	// 创建 Roadmap
	steps := []RoadmapStep{
		{TaskID: taskID, Step: 1.0, Objective: "测试 SQL 注入", Status: StepActive},
	}
	err := store.SaveRoadmap(ctx, taskID, steps)
	require.NoError(t, err)

	// 为 Step 1.0 创建多个 Action
	step := 1.0
	state := StateOpen

	action1 := Node{
		ID:          "action1",
		TaskID:      taskID,
		Kind:        KindAction,
		Content:     []byte(`{"instruction": "测试 UNION 注入"}`),
		State:       &state,
		RoadmapStep: &step,
	}

	action2 := Node{
		ID:          "action2",
		TaskID:      taskID,
		Kind:        KindAction,
		Content:     []byte(`{"instruction": "测试布尔盲注"}`),
		State:       &state,
		RoadmapStep: &step,
	}

	_, err = store.CreateNode(ctx, action1)
	require.NoError(t, err)
	_, err = store.CreateNode(ctx, action2)
	require.NoError(t, err)

	// 查询 Step 1.0 的所有 Action
	actions, err := store.ListActionsByRoadmapStep(ctx, taskID, 1.0)
	require.NoError(t, err)
	assert.Equal(t, 2, len(actions))

	// Step 1.0 还未完成（Action 是 open 状态）
	isComplete, err := store.IsRoadmapStepComplete(ctx, taskID, 1.0)
	require.NoError(t, err)
	assert.False(t, isComplete)

	// 标记所有 Action 为 done
	doneState := StateDone
	action1.State = &doneState
	action2.State = &doneState
	err = store.UpdateActionState(ctx, "action1", StateDone, nil)
	require.NoError(t, err)
	err = store.UpdateActionState(ctx, "action2", StateDone, nil)
	require.NoError(t, err)

	// 现在 Step 1.0 完成了
	isComplete, err = store.IsRoadmapStepComplete(ctx, taskID, 1.0)
	require.NoError(t, err)
	assert.True(t, isComplete)
}
