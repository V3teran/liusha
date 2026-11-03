package runtime

import (
	"context"
	"testing"

	"github.com/V3teran/liusha/internal/framework/llm"
)

func TestValidateModifier(t *testing.T) {
	modifier := NewValidateModifier(100)

	t.Run("Valid messages", func(t *testing.T) {
		messages := []llm.Message{
			{Role: llm.RoleUser, Content: "短消息"},
			{Role: llm.RoleAssistant, Content: "回复"},
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

		messages := []llm.Message{
			{Role: llm.RoleUser, Content: string(longContent)},
		}

		_, err := modifier.Modify(context.Background(), messages)
		if err == nil {
			t.Error("expected error for too long message, got nil")
		}
	})

	t.Run("Empty role", func(t *testing.T) {
		messages := []llm.Message{
			{Role: "", Content: "无角色"},
		}

		_, err := modifier.Modify(context.Background(), messages)
		if err == nil {
			t.Error("expected error for empty role, got nil")
		}
	})
}

func TestMessageModifierChain(t *testing.T) {
	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: "系统提示"},
		{Role: llm.RoleUser, Content: "用户消息1"},
		{Role: llm.RoleAssistant, Content: "助手消息1"},
		{Role: llm.RoleUser, Content: "用户消息2"},
		{Role: llm.RoleAssistant, Content: "助手消息2"},
		{Role: llm.RoleUser, Content: "用户消息3"},
	}

	t.Run("FailFast strategy", func(t *testing.T) {
		chain := NewCompactionChain(8, "test", nil) // 预算 8 token：朴素保留装不下全量历史

		result, err := chain.Apply(context.Background(), messages)
		if err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		// 压缩生效：消息数变少，且压缩后首条非 system 是 user（占位/纪要）
		if len(result) >= len(messages) {
			t.Errorf("expected compaction, got %d messages (input %d)", len(result), len(messages))
		}
		if result[1].Role != llm.RoleUser {
			t.Errorf("压缩后首条非 system 应为 user, got %s", result[1].Role)
		}
	})

	t.Run("Skip strategy", func(t *testing.T) {
		// 创建一个会失败的修改器
		failingModifier := &mockFailingModifier{}

		chain := NewMessageModifierChain(
			[]MessageModifier{
				failingModifier,
				NewContextCompactor(8, "test", nil), // 跳过失败者后压缩仍应执行
			},
			ErrorStrategySkip,
			RetryConfig{MaxAttempts: 0},
			10,
		)

		result, err := chain.Apply(context.Background(), messages)
		if err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		if len(result) >= len(messages) {
			t.Errorf("expected compaction after skipping failed modifier, got %d messages", len(result))
		}
	})
}

func TestCompactionChain_ValidationSentinel(t *testing.T) {
	// 压缩之后的长度校验哨兵：单条超长消息越线即失败（fail-fast）——
	// 压缩解决体量问题，哨兵守住不变量。
	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: "系统提示"},
		{Role: llm.RoleUser, Content: string(make([]byte, 100001))},
	}
	chain := NewCompactionChain(1<<20, "test", nil)
	if _, err := chain.Apply(context.Background(), messages); err == nil {
		t.Error("expected validation error for oversized message, got nil")
	}
}

// mockFailingModifier 总是失败的修改器（用于测试）
type mockFailingModifier struct{}

func (m *mockFailingModifier) Modify(_ context.Context, _ []llm.Message) ([]llm.Message, error) {
	return nil, context.Canceled
}
