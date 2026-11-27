package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/V3teran/liusha/internal/framework/llm"
)

// 单条消息的上限按字节计（中文一个字三个字节）；越线以 ErrMessageTooLong 失败，调用方据此
// 立刻终止而不是重试。上限本身也必须是校验哨兵实际用的那个数。
func TestValidateModifier_TooLongMessageIsASentinel(t *testing.T) {
	m := NewValidateModifier(0)
	atLimit := llm.Message{Role: llm.RoleUser, Content: strings.Repeat("a", MaxMessageBytes)}
	if _, err := m.Modify(context.Background(), []llm.Message{atLimit}); err != nil {
		t.Fatalf("恰好到上限应通过: %v", err)
	}
	over := llm.Message{Role: llm.RoleUser, Content: strings.Repeat("字", MaxMessageBytes/3+1)}
	_, err := m.Modify(context.Background(), []llm.Message{over})
	if !errors.Is(err, ErrMessageTooLong) {
		t.Fatalf("超长应以 ErrMessageTooLong 失败，得 %v", err)
	}
	if len(over.Content) <= MaxMessageBytes {
		t.Fatalf("测试样本应超限: %d 字节", len(over.Content))
	}
}

// 预设的压缩链用的是同一个上限：规划摘要的预算以它为准。
func TestCompactionChain_UsesMaxMessageBytes(t *testing.T) {
	chain := NewCompactionChain(1<<20, "planner", nil, CompactionTimeouts{Distill: 10, Chain: 15})
	_, err := chain.Apply(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: strings.Repeat("a", MaxMessageBytes+1)}})
	if !errors.Is(err, ErrMessageTooLong) {
		t.Fatalf("压缩链末尾的校验应以 ErrMessageTooLong 拒绝超长消息，得 %v", err)
	}
}
