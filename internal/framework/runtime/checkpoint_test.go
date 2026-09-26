package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/framework/persistence/memory"
)

// testMockProvider 用于测试的 Mock LLM Provider
type testMockProvider struct {
	Responses []string
	Index     int
	NumCalls  int
}

func (p *testMockProvider) Complete(ctx context.Context, req llm.Request) (llm.Response, error) {
	p.NumCalls++
	resp := llm.Response{
		Content:   p.Responses[p.Index%len(p.Responses)],
		ToolCalls: nil,
	}

	// 前 n-1 次迭代返回工具调用，强制多次迭代
	if p.Index < len(p.Responses)-1 {
		resp.ToolCalls = []llm.ToolCall{
			{
				ID:        "tool_call_1",
				Name:      "test_tool",
				Arguments: json.RawMessage(`{"input": "test"}`),
			},
		}
	}

	p.Index++
	return resp, nil
}

func (p *testMockProvider) Stream(ctx context.Context, req llm.Request) (<-chan llm.StreamEvent, error) {
	ch := make(chan llm.StreamEvent, 1)
	go func() {
		defer close(ch)
		ch <- llm.StreamEvent{
			Kind:    llm.StreamText,
			Content: p.Responses[p.Index%len(p.Responses)],
		}
		p.Index++
		ch <- llm.StreamEvent{
			Kind: llm.StreamDone,
		}
	}()
	return ch, nil
}

func (p *testMockProvider) CountTokens(ctx context.Context, req llm.Request) (int, error) {
	return 100, nil
}

func (p *testMockProvider) ModelID() string {
	return "test-model"
}

func (p *testMockProvider) ProviderID() string {
	return "test"
}

// TestReActRuntime_CheckpointSave 测试检查点保存
func TestReActRuntime_CheckpointSave(t *testing.T) {
	ctx := context.Background()
	mockProvider := &testMockProvider{
		Responses: []string{
			"这是第一次迭代的回答",
			"这是第二次迭代的回答",
			"这是第三次迭代的回答",
			"这是第四次迭代的回答",
			"Final answer: 任务完成",
		},
	}

	checkpointer := memory.NewCheckpointer()
	policy := NewIterationCheckpointPolicy(2) // 每2次迭代保存一次

	runtime := NewReActRuntime()
	config := &ReActConfig{
		Objective:        "完成测试任务",
		SystemPrompt:     "你是测试助手",
		LLMProvider:      mockProvider,
		MaxIterations:    5,
		Temperature:      0.7,
		MaxTokens:        1000,
		TaskID:           "test-task-001",
		Checkpointer:     checkpointer,
		CheckpointPolicy: policy,
	}

	result, err := runtime.Run(ctx, config)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// 验证检查点已保存
	if len(result.CheckpointIDs) == 0 {
		t.Fatal("Expected checkpoints to be saved, but got none")
	}

	// 验证检查点数量（5次迭代，每2次保存一次 = 2个检查点：第2次和第4次）
	expectedCheckpoints := 2
	if len(result.CheckpointIDs) != expectedCheckpoints {
		t.Errorf("Expected %d checkpoints, got %d", expectedCheckpoints, len(result.CheckpointIDs))
	}

	// 验证最后的检查点ID已记录
	if result.CheckpointID == "" {
		t.Error("Expected CheckpointID to be set")
	}

	// 验证可以加载检查点
	checkpoint, err := checkpointer.Load(ctx, result.CheckpointID)
	if err != nil {
		t.Fatalf("Load checkpoint failed: %v", err)
	}

	if checkpoint.TaskID != config.TaskID {
		t.Errorf("Expected TaskID %s, got %s", config.TaskID, checkpoint.TaskID)
	}
}

// TestReActRuntime_CheckpointRestore 测试从检查点恢复
func TestReActRuntime_CheckpointRestore(t *testing.T) {
	ctx := context.Background()
	mockProvider := &testMockProvider{
		Responses: []string{
			"第1次迭代",
			"第2次迭代",
			"第3次迭代 - 中断前",
			"第4次迭代 - 恢复后",
			"Final answer: 完成",
		},
	}

	checkpointer := memory.NewCheckpointer()
	policy := NewIterationCheckpointPolicy(1) // 每次迭代都保存

	runtime := NewReActRuntime()

	// 第一次运行：执行3次迭代后模拟中断
	config1 := &ReActConfig{
		Objective:        "完成长时间任务",
		SystemPrompt:     "你是测试助手",
		LLMProvider:      mockProvider,
		MaxIterations:    3,
		Temperature:      0.7,
		MaxTokens:        1000,
		TaskID:           "test-task-002",
		Checkpointer:     checkpointer,
		CheckpointPolicy: policy,
	}

	result1, err := runtime.Run(ctx, config1)
	if err != nil {
		t.Fatalf("First run failed: %v", err)
	}

	if result1.Iterations != 3 {
		t.Errorf("Expected 3 iterations, got %d", result1.Iterations)
	}

	lastCheckpoint := result1.CheckpointID
	if lastCheckpoint == "" {
		t.Fatal("Expected checkpoint to be saved")
	}

	// 第二次运行：从检查点恢复，继续执行
	config2 := &ReActConfig{
		Objective:             "完成长时间任务",
		SystemPrompt:          "你是测试助手",
		LLMProvider:           mockProvider,
		MaxIterations:         5,
		Temperature:           0.7,
		MaxTokens:             1000,
		TaskID:                "test-task-002",
		Checkpointer:          checkpointer,
		CheckpointPolicy:      policy,
		RestoreFromCheckpoint: lastCheckpoint,
	}

	result2, err := runtime.Run(ctx, config2)
	if err != nil {
		t.Fatalf("Second run failed: %v", err)
	}

	// 验证恢复信息
	if result2.RestoredFromCheckpoint != lastCheckpoint {
		t.Errorf("Expected restored checkpoint %s, got %s", lastCheckpoint, result2.RestoredFromCheckpoint)
	}

	if result2.RestoredIteration != 3 {
		t.Errorf("Expected restored iteration 3, got %d", result2.RestoredIteration)
	}

	// 验证继续执行（从第4次迭代开始，最多到第5次）
	if result2.Iterations < 4 {
		t.Errorf("Expected at least 4 iterations after restore, got %d", result2.Iterations)
	}

	// 验证消息历史包含恢复前的内容
	if len(result2.MessageHistory) < len(result1.MessageHistory) {
		t.Error("Expected restored message history to include previous messages")
	}
}

// TestReActRuntime_CheckpointPolicy 测试不同的检查点策略
func TestReActRuntime_CheckpointPolicy(t *testing.T) {
	ctx := context.Background()

	checkpointer := memory.NewCheckpointer()

	tests := []struct {
		name              string
		policy            CheckpointPolicy
		expectedSaveCount int
	}{
		{
			name:              "IterationPolicy_Every2",
			policy:            NewIterationCheckpointPolicy(2),
			expectedSaveCount: 1, // 3次迭代，每2次保存 = 第2次保存
		},
		{
			name:              "AlwaysPolicy",
			policy:            NewAlwaysCheckpointPolicy(),
			expectedSaveCount: 3, // 每次迭代都保存
		},
		{
			name:              "NeverPolicy",
			policy:            NewNeverCheckpointPolicy(),
			expectedSaveCount: 0, // 从不保存
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 为每个测试用例创建独立的 mockProvider
			testMockProvider := &testMockProvider{
				Responses: []string{
					"迭代1",
					"迭代2",
					"迭代3",
				},
			}

			runtime := NewReActRuntime()
			config := &ReActConfig{
				Objective:        "测试任务",
				SystemPrompt:     "你是测试助手",
				LLMProvider:      testMockProvider,
						MaxIterations:    3,
				Temperature:      0.7,
				MaxTokens:        1000,
				TaskID:           "test-task-" + tt.name,
				Checkpointer:     checkpointer,
				CheckpointPolicy: tt.policy,
			}

			result, err := runtime.Run(ctx, config)
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}

			if len(result.CheckpointIDs) != tt.expectedSaveCount {
				t.Errorf("Expected %d checkpoints, got %d, iterations: %d", tt.expectedSaveCount, len(result.CheckpointIDs), result.Iterations)
			}
		})
	}
}

// TestReActRuntime_CheckpointPrune 测试检查点清理
func TestReActRuntime_CheckpointPrune(t *testing.T) {
	ctx := context.Background()
	checkpointer := memory.NewCheckpointer()
	taskID := "test-task-prune"

	// 保存5个检查点
	for i := 1; i <= 5; i++ {
		checkpoint := core.Checkpoint{
			TaskID:        taskID,
			StateSnapshot: []byte(`{"iteration": ` + fmt.Sprintf("%d", i) + `}`),
			Phase:         "test",
			CreatedAt:     time.Now().Add(time.Duration(i) * time.Second),
		}
		_, err := checkpointer.Save(ctx, checkpoint)
		if err != nil {
			t.Fatalf("Save checkpoint failed: %v", err)
		}
	}

	// 验证保存了5个
	list, err := checkpointer.List(ctx, taskID, 0)
	if err != nil {
		t.Fatalf("List checkpoints failed: %v", err)
	}
	if len(list) != 5 {
		t.Errorf("Expected 5 checkpoints, got %d", len(list))
	}

	// 清理，只保留最近3个
	err = checkpointer.Prune(ctx, taskID, 3)
	if err != nil {
		t.Fatalf("Prune checkpoints failed: %v", err)
	}

	// 验证只剩3个
	list, err = checkpointer.List(ctx, taskID, 0)
	if err != nil {
		t.Fatalf("List checkpoints after prune failed: %v", err)
	}
	if len(list) != 3 {
		t.Errorf("Expected 3 checkpoints after prune, got %d", len(list))
	}
}
