package core

import (
	"context"
	"testing"
)

func TestBufferMemory(t *testing.T) {
	ctx := context.Background()

	t.Run("添加和获取消息", func(t *testing.T) {
		memory := NewBufferMemory(5)

		msg1 := *NewTextMessage("1", RoleUser, "你好")
		msg2 := *NewTextMessage("2", RoleAssistant, "你好，有什么可以帮助你的吗？")

		if err := memory.AddMessage(ctx, msg1); err != nil {
			t.Fatalf("添加消息失败: %v", err)
		}
		if err := memory.AddMessage(ctx, msg2); err != nil {
			t.Fatalf("添加消息失败: %v", err)
		}

		messages, err := memory.GetMessages(ctx)
		if err != nil {
			t.Fatalf("获取消息失败: %v", err)
		}

		if len(messages) != 2 {
			t.Errorf("期望 2 条消息，实际 %d 条", len(messages))
		}
	})

	t.Run("超过最大数量自动移除", func(t *testing.T) {
		memory := NewBufferMemory(3)

		for i := 0; i < 5; i++ {
			msg := *NewTextMessage(string(rune('1'+i)), RoleUser, "消息")
			_ = memory.AddMessage(ctx, msg)
		}

		messages, _ := memory.GetMessages(ctx)
		if len(messages) != 3 {
			t.Errorf("期望 3 条消息，实际 %d 条", len(messages))
		}
	})

	t.Run("保留 system 消息", func(t *testing.T) {
		memory := NewBufferMemory(3)

		systemMsg := *NewTextMessage("sys", RoleSystem, "你是一个助手")
		_ = memory.AddMessage(ctx, systemMsg)

		for i := 0; i < 5; i++ {
			msg := *NewTextMessage(string(rune('1'+i)), RoleUser, "消息")
			_ = memory.AddMessage(ctx, msg)
		}

		messages, _ := memory.GetMessages(ctx)

		// 检查 system 消息是否保留
		hasSystem := false
		for _, msg := range messages {
			if msg.Role == RoleSystem {
				hasSystem = true
				break
			}
		}

		if !hasSystem {
			t.Error("system 消息应该被保留")
		}
	})

	t.Run("按 token 预算获取消息", func(t *testing.T) {
		memory := NewBufferMemory(10)

		// 添加 system 消息
		systemMsg := *NewTextMessage("sys", RoleSystem, "你是一个助手")
		_ = memory.AddMessage(ctx, systemMsg)

		// 添加用户消息
		for i := 0; i < 5; i++ {
			msg := NewTextMessage(string(rune('1'+i)), RoleUser, "消息")
			msg.Metadata.TokenCount = 20
			_ = memory.AddMessage(ctx, *msg)
		}

		// 预算 60 tokens：system(10) + 最近2条消息(40)
		messages, err := memory.GetRecentMessages(ctx, 60)
		if err != nil {
			t.Fatalf("获取消息失败: %v", err)
		}

		// system 消息 + 2 条用户消息
		if len(messages) != 3 {
			t.Errorf("期望 3 条消息，实际 %d 条", len(messages))
		}

		// 验证 system 消息在前
		if messages[0].Role != RoleSystem {
			t.Error("system 消息应该在最前面")
		}
	})

	t.Run("清空记忆", func(t *testing.T) {
		memory := NewBufferMemory(5)

		msg := *NewTextMessage("1", RoleUser, "你好")
		_ = memory.AddMessage(ctx, msg)

		if err := memory.Clear(ctx); err != nil {
			t.Fatalf("清空失败: %v", err)
		}

		if memory.Size() != 0 {
			t.Errorf("清空后应该为 0，实际 %d", memory.Size())
		}
	})
}

func TestWindowMemory(t *testing.T) {
	ctx := context.Background()

	t.Run("滑动窗口", func(t *testing.T) {
		memory := NewWindowMemory(100) // 100 tokens 预算

		// 添加消息直到超过预算
		for i := 0; i < 10; i++ {
			msg := *NewTextMessage(string(rune('1' + i)), RoleUser, "消息")
			_ = memory.AddMessage(ctx, msg)
		}

		messages, _ := memory.GetMessages(ctx)

		// 应该只保留最近的 5 条（100 / 20 = 5）
		if len(messages) > 5 {
			t.Errorf("超过预算，期望 <= 5 条消息，实际 %d 条", len(messages))
		}
	})

	t.Run("保留 system 消息", func(t *testing.T) {
		memory := NewWindowMemory(50)

		systemMsg := *NewTextMessage("sys", RoleSystem, "你是一个助手")
		_ = memory.AddMessage(ctx, systemMsg)

		// 添加超过预算的消息
		for i := 0; i < 5; i++ {
			msg := *NewTextMessage(string(rune('1' + i)), RoleUser, "消息")
			_ = memory.AddMessage(ctx, msg)
		}

		messages, _ := memory.GetMessages(ctx)

		// 检查 system 消息是否保留
		hasSystem := false
		for _, msg := range messages {
			if msg.Role == RoleSystem {
				hasSystem = true
				break
			}
		}

		if !hasSystem {
			t.Error("system 消息应该被保留")
		}
	})
}

func TestSummaryMemory(t *testing.T) {
	ctx := context.Background()

	// Mock summarizer
	mockSummarizer := &mockSummarizer{}

	t.Run("超过阈值触发摘要", func(t *testing.T) {
		memory := NewSummaryMemory(mockSummarizer, 10, 3)

		// 添加 5 条消息，应该触发一次摘要
		for i := 0; i < 5; i++ {
			msg := *NewTextMessage(string(rune('1'+i)), RoleUser, "消息")
			_ = memory.AddMessage(ctx, msg)
		}

		messages, _ := memory.GetMessages(ctx)

		// 应该有摘要 + 最近的消息
		hasSummary := false
		for _, msg := range messages {
			if msg.Role == RoleSystem && len(msg.Content) > 0 {
				hasSummary = true
				break
			}
		}

		if !hasSummary {
			t.Error("应该生成摘要")
		}

		// 最近的消息应该保留
		if memory.Size() > 3 {
			t.Errorf("触发摘要后，期望保留 <= 3 条最近消息，实际 %d 条", memory.Size())
		}
	})
}

// mockSummarizer 模拟摘要生成器
type mockSummarizer struct{}

func (m *mockSummarizer) Summarize(ctx context.Context, messages []Message) (string, error) {
	return "这是一段对话摘要", nil
}

func TestMessageTokenCount(t *testing.T) {
	t.Run("估算 token 数量", func(t *testing.T) {
		msg := NewTextMessage("test-1", RoleUser, "这是一段测试文本")

		tokens := msg.TokenCount()
		if tokens <= 0 {
			t.Error("token 数量应该大于 0")
		}
	})

	t.Run("使用元数据中的 token 数量", func(t *testing.T) {
		msg := NewTextMessage("test-2", RoleUser, "测试")
		msg.Metadata.TokenCount = 100

		tokens := msg.TokenCount()
		if tokens != 100 {
			t.Errorf("应该使用元数据中的值 100，实际 %d", tokens)
		}
	})
}

func TestMessageClone(t *testing.T) {
	original := *NewTextMessage("1", RoleUser, "原始消息")

	cloned := original.Clone()

	if cloned.ID != original.ID {
		t.Error("克隆的消息 ID 应该相同")
	}

	// 修改克隆的消息不应影响原始消息
	cloned.Content = "修改后的消息"

	if original.Content == cloned.Content {
		t.Error("修改克隆消息不应影响原始消息")
	}
}
