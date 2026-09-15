package runtime

import (
	"context"
	"testing"
)

func TestRollingWindowModifier(t *testing.T) {
	modifier := NewRollingWindowModifier(3)

	messages := []*Message{
		{Role: MessageRoleSystem, Content: "系统提示1"},
		{Role: MessageRoleSystem, Content: "系统提示2"},
		{Role: MessageRoleUser, Content: "用户消息1"},
		{Role: MessageRoleAssistant, Content: "助手消息1"},
		{Role: MessageRoleUser, Content: "用户消息2"},
		{Role: MessageRoleAssistant, Content: "助手消息2"},
		{Role: MessageRoleUser, Content: "用户消息3"},
		{Role: MessageRoleAssistant, Content: "助手消息3"},
		{Role: MessageRoleUser, Content: "用户消息4"},
	}

	result, err := modifier.Modify(context.Background(), messages)
	if err != nil {
		t.Fatalf("Modify failed: %v", err)
	}

	// 应该保留：2条系统消息 + 最近3条非系统消息
	if len(result) != 5 {
		t.Errorf("expected 5 messages, got %d", len(result))
	}

	// 验证系统消息都在
	systemCount := 0
	for _, msg := range result {
		if msg.Role == MessageRoleSystem {
			systemCount++
		}
	}
	if systemCount != 2 {
		t.Errorf("expected 2 system messages, got %d", systemCount)
	}

	// 验证最后一条是用户消息4
	if result[len(result)-1].Content != "用户消息4" {
		t.Errorf("expected last message to be '用户消息4', got '%s'", result[len(result)-1].Content)
	}
}

func TestTruncateModifier(t *testing.T) {
	modifier := NewTruncateModifier(3)

	messages := []*Message{
		{Role: MessageRoleSystem, Content: "系统提示"},
		{Role: MessageRoleUser, Content: "用户消息1"},
		{Role: MessageRoleAssistant, Content: "助手消息1"},
		{Role: MessageRoleUser, Content: "用户消息2"},
		{Role: MessageRoleAssistant, Content: "助手消息2"},
		{Role: MessageRoleUser, Content: "用户消息3"},
	}

	result, err := modifier.Modify(context.Background(), messages)
	if err != nil {
		t.Fatalf("Modify failed: %v", err)
	}

	// 应该保留：系统消息 + 最近3条
	if len(result) != 4 {
		t.Errorf("expected 4 messages, got %d", len(result))
	}

	// 第一条应该是系统消息
	if result[0].Role != MessageRoleSystem {
		t.Errorf("expected first message to be system, got %s", result[0].Role)
	}
}

func TestValidateModifier(t *testing.T) {
	modifier := NewValidateModifier(100)

	t.Run("Valid messages", func(t *testing.T) {
		messages := []*Message{
			{Role: MessageRoleUser, Content: "短消息"},
			{Role: MessageRoleAssistant, Content: "回复"},
		}

		_, err := modifier.Modify(context.Background(), messages)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})

	t.Run("Message too long", func(t *testing.T) {
		longContent := make([]byte, 101)
		for i := range longContent {
			longContent[i] = 'a'
		}

		messages := []*Message{
			{Role: MessageRoleUser, Content: string(longContent)},
		}

		_, err := modifier.Modify(context.Background(), messages)
		if err == nil {
			t.Error("expected error for too long message, got nil")
		}
	})

	t.Run("Empty role", func(t *testing.T) {
		messages := []*Message{
			{Role: "", Content: "无角色"},
		}

		_, err := modifier.Modify(context.Background(), messages)
		if err == nil {
			t.Error("expected error for empty role, got nil")
		}
	})
}

func TestMessageModifierChain(t *testing.T) {
	messages := []*Message{
		{Role: MessageRoleSystem, Content: "系统提示"},
		{Role: MessageRoleUser, Content: "用户消息1"},
		{Role: MessageRoleAssistant, Content: "助手消息1"},
		{Role: MessageRoleUser, Content: "用户消息2"},
		{Role: MessageRoleAssistant, Content: "助手消息2"},
		{Role: MessageRoleUser, Content: "用户消息3"},
	}

	t.Run("FailFast strategy", func(t *testing.T) {
		chain := NewMessageModifierChain(
			[]MessageModifier{
				NewValidateModifier(100),
				NewTruncateModifier(2),
			},
			ErrorStrategyFailFast,
			RetryConfig{MaxAttempts: 0},
			10,
		)

		result, err := chain.Apply(context.Background(), messages)
		if err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		// 验证截断生效
		if len(result) != 3 { // 系统消息 + 2条
			t.Errorf("expected 3 messages, got %d", len(result))
		}
	})

	t.Run("Skip strategy", func(t *testing.T) {
		// 创建一个会失败的修改器
		failingModifier := &mockFailingModifier{}

		chain := NewMessageModifierChain(
			[]MessageModifier{
				failingModifier,
				NewTruncateModifier(2), // 这个应该继续执行
			},
			ErrorStrategySkip,
			RetryConfig{MaxAttempts: 0},
			10,
		)

		result, err := chain.Apply(context.Background(), messages)
		if err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		// 跳过失败的修改器，截断应该仍然生效
		if len(result) != 3 {
			t.Errorf("expected 3 messages after skipping failed modifier, got %d", len(result))
		}
	})
}

func TestPredefinedModifierChains(t *testing.T) {
	messages := []*Message{
		{Role: MessageRoleSystem, Content: "系统提示"},
	}
	for i := 0; i < 50; i++ {
		messages = append(messages, &Message{
			Role:    MessageRoleUser,
			Content: "用户消息",
		})
	}

	t.Run("PlannerModifierChain", func(t *testing.T) {
		chain := NewPlannerModifierChain()
		result, err := chain.Apply(context.Background(), messages)
		if err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		// Planner 保留30条 + 系统消息
		if len(result) != 31 {
			t.Errorf("expected 31 messages, got %d", len(result))
		}
	})

	t.Run("ExecutorModifierChain", func(t *testing.T) {
		chain := NewExecutorModifierChain()
		result, err := chain.Apply(context.Background(), messages)
		if err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		// Executor 保留15条 + 系统消息
		if len(result) != 16 {
			t.Errorf("expected 16 messages, got %d", len(result))
		}
	})

	t.Run("EvaluatorModifierChain", func(t *testing.T) {
		chain := NewEvaluatorModifierChain()
		result, err := chain.Apply(context.Background(), messages)
		if err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		// Evaluator 保留25条 + 系统消息
		if len(result) != 26 {
			t.Errorf("expected 26 messages, got %d", len(result))
		}
	})

	t.Run("MonitorModifierChain", func(t *testing.T) {
		chain := NewMonitorModifierChain()
		result, err := chain.Apply(context.Background(), messages)
		if err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		// Monitor 保留10条 + 系统消息
		if len(result) != 11 {
			t.Errorf("expected 11 messages, got %d", len(result))
		}
	})
}

// mockFailingModifier 总是失败的修改器（用于测试）
type mockFailingModifier struct{}

func (m *mockFailingModifier) Modify(ctx context.Context, messages []*Message) ([]*Message, error) {
	return nil, context.Canceled
}
