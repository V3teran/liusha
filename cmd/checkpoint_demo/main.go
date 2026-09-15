package main

import (
	"context"
	"fmt"
	"time"

	"github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/framework/persistence/memory"
	"github.com/V3teran/liusha/internal/framework/runtime"
)

// SimpleMockProvider 是简单的 Mock LLM Provider
type SimpleMockProvider struct {
	Responses []string
	Index     int
}

func (p *SimpleMockProvider) Complete(ctx context.Context, req llm.Request) (llm.Response, error) {
	resp := llm.Response{
		Content:   p.Responses[p.Index%len(p.Responses)],
		ToolCalls: nil,
	}
	p.Index++
	return resp, nil
}

func (p *SimpleMockProvider) Stream(ctx context.Context, req llm.Request) (<-chan llm.StreamEvent, error) {
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

func (p *SimpleMockProvider) CountTokens(ctx context.Context, req llm.Request) (int, error) {
	return 100, nil
}

func (p *SimpleMockProvider) ModelID() string {
	return "mock-model"
}

func (p *SimpleMockProvider) ProviderID() string {
	return "mock"
}

func main() {
	ctx := context.Background()

	// 创建 Mock LLM Provider
	mockProvider := &SimpleMockProvider{
		Responses: []string{
			"第1次迭代：开始任务",
			"第2次迭代：执行中",
			"第3次迭代：完成任务",
		},
	}

	// 创建内存版 Checkpointer
	checkpointer := memory.NewCheckpointer()

	// 创建检查点策略（每次迭代都保存）
	policy := runtime.NewIterationCheckpointPolicy(1)

	// 创建 ReActRuntime
	reactRuntime := runtime.NewReActRuntime()

	// 配置
	config := &runtime.ReActConfig{
		Objective:        "完成测试任务",
		SystemPrompt:     "你是一个测试助手",
		LLMProvider:      mockProvider,
		ModelID:          "test-model",
		MaxIterations:    3,
		Temperature:      0.7,
		MaxTokens:        1000,
		TaskID:           "demo-task-001",
		Checkpointer:     checkpointer,
		CheckpointPolicy: policy,
		OnIteration: func(iteration int, status runtime.IterationStatus) {
			fmt.Printf("[Iteration %d] Status: %s\n", iteration, status)
		},
	}

	// 运行
	fmt.Println("=== 开始执行 ReAct 循环 ===")
	result, err := reactRuntime.Run(ctx, config)
	if err != nil {
		fmt.Printf("执行失败: %v\n", err)
		return
	}

	// 输出结果
	fmt.Printf("\n=== 执行完成 ===\n")
	fmt.Printf("状态: %s\n", result.Status)
	fmt.Printf("迭代次数: %d\n", result.Iterations)
	fmt.Printf("保存的检查点数量: %d\n", len(result.CheckpointIDs))
	fmt.Printf("最后的检查点ID: %s\n", result.CheckpointID)

	// 验证检查点
	if result.CheckpointID != "" {
		checkpoint, err := checkpointer.Load(ctx, result.CheckpointID)
		if err != nil {
			fmt.Printf("加载检查点失败: %v\n", err)
			return
		}
		fmt.Printf("\n=== 检查点详情 ===\n")
		fmt.Printf("检查点ID: %s\n", checkpoint.ID)
		fmt.Printf("任务ID: %s\n", checkpoint.TaskID)
		fmt.Printf("阶段: %s\n", checkpoint.Phase)
		fmt.Printf("创建时间: %s\n", checkpoint.CreatedAt.Format(time.RFC3339))
		fmt.Printf("大小: %d bytes\n", checkpoint.SizeBytes)
	}

	// 测试恢复
	fmt.Println("\n=== 测试从检查点恢复 ===")
	config2 := &runtime.ReActConfig{
		Objective:             "从检查点继续",
		SystemPrompt:          "你是一个测试助手",
		LLMProvider:           mockProvider,
		ModelID:               "test-model",
		MaxIterations:         5,
		Temperature:           0.7,
		MaxTokens:             1000,
		TaskID:                "demo-task-001",
		Checkpointer:          checkpointer,
		CheckpointPolicy:      policy,
		RestoreFromCheckpoint: result.CheckpointID,
	}

	result2, err := reactRuntime.Run(ctx, config2)
	if err != nil {
		fmt.Printf("恢复执行失败: %v\n", err)
		return
	}

	fmt.Printf("恢复自检查点: %s\n", result2.RestoredFromCheckpoint)
	fmt.Printf("恢复的迭代: %d\n", result2.RestoredIteration)
	fmt.Printf("执行的迭代: %d\n", result2.Iterations)

	fmt.Println("\n✅ Checkpoint 功能验证成功！")
}
