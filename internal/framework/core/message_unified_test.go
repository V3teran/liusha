package core

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/framework/llm"
)

func TestNewMessage(t *testing.T) {
	llmMsg := llm.Message{
		Role:    llm.RoleUser,
		Content: "test content",
	}

	msg := NewMessage("msg-1", llmMsg)

	if msg.ID != "msg-1" {
		t.Errorf("ID = %q, want %q", msg.ID, "msg-1")
	}
	if msg.Role != llm.RoleUser {
		t.Errorf("Role = %q, want %q", msg.Role, llm.RoleUser)
	}
	if msg.Content != "test content" {
		t.Errorf("Content = %q, want %q", msg.Content, "test content")
	}
	if msg.CreatedAt.IsZero() {
		t.Error("CreatedAt should not be zero")
	}
}

func TestNewTextMessage(t *testing.T) {
	msg := NewTextMessage("msg-2", llm.RoleAssistant, "hello world")

	if msg.ID != "msg-2" {
		t.Errorf("ID = %q, want %q", msg.ID, "msg-2")
	}
	if msg.Role != llm.RoleAssistant {
		t.Errorf("Role = %q, want %q", msg.Role, llm.RoleAssistant)
	}
	if msg.Content != "hello world" {
		t.Errorf("Content = %q, want %q", msg.Content, "hello world")
	}
}

func TestNewMultimodalMessage(t *testing.T) {
	parts := []llm.ContentPart{
		{Type: "text", Text: "描述图片"},
		{Type: "image", ImageData: &llm.ImageContent{MediaType: "image/png", Base64Data: "base64data"}},
	}

	msg := NewMultimodalMessage("msg-3", llm.RoleUser, parts)

	if msg.ID != "msg-3" {
		t.Errorf("ID = %q, want %q", msg.ID, "msg-3")
	}
	if len(msg.Parts) != 2 {
		t.Errorf("len(Parts) = %d, want 2", len(msg.Parts))
	}
	if msg.Parts[0].Type != "text" {
		t.Errorf("Parts[0].Type = %q, want %q", msg.Parts[0].Type, "text")
	}
	if msg.Parts[1].Type != "image" {
		t.Errorf("Parts[1].Type = %q, want %q", msg.Parts[1].Type, "image")
	}
}

func TestMessageTokenCountUnified(t *testing.T) {
	tests := []struct {
		name     string
		msg      *Message
		wantMin  int
		wantMax  int
		metadata bool
	}{
		{
			name: "纯文本估算",
			msg: &Message{
				Message: llm.Message{
					Content: "1234567890123456", // 16 字符 ≈ 4 tokens
				},
			},
			wantMin: 3,
			wantMax: 5,
		},
		{
			name: "元数据优先",
			msg: &Message{
				Message: llm.Message{
					Content: "content",
				},
				Metadata: MessageMetadata{
					TokenCount: 100,
				},
			},
			wantMin:  100,
			wantMax:  100,
			metadata: true,
		},
		{
			name: "多模态估算",
			msg: &Message{
				Message: llm.Message{
					Parts: []llm.ContentPart{
						{Type: "text", Text: "12345678"}, // 8 字符 ≈ 2 tokens
						{Type: "image"},                   // 固定 85 tokens
					},
				},
			},
			wantMin: 85,
			wantMax: 90,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.msg.TokenCount()
			if got < tt.wantMin || got > tt.wantMax {
				t.Errorf("TokenCount() = %d, want in range [%d, %d]", got, tt.wantMin, tt.wantMax)
			}
		})
	}
}

func TestMessageCloneUnified(t *testing.T) {
	original := &Message{
		ID: "msg-1",
		Message: llm.Message{
			Role:    llm.RoleUser,
			Content: "original",
			ToolCalls: []llm.ToolCall{
				{ID: "call-1", Name: "search", Arguments: json.RawMessage(`{"q":"test"}`)},
			},
		},
		Metadata: MessageMetadata{
			TokenCount: 50,
			Tags:       map[string]string{"key": "value"},
		},
		CreatedAt: time.Now(),
	}

	cloned := original.Clone()

	// 验证值相等
	if cloned.ID != original.ID {
		t.Errorf("cloned.ID = %q, want %q", cloned.ID, original.ID)
	}
	if cloned.Content != original.Content {
		t.Errorf("cloned.Content = %q, want %q", cloned.Content, original.Content)
	}
	if cloned.Metadata.TokenCount != original.Metadata.TokenCount {
		t.Errorf("cloned.Metadata.TokenCount = %d, want %d", cloned.Metadata.TokenCount, original.Metadata.TokenCount)
	}

	// 验证深拷贝
	cloned.Content = "modified"
	if original.Content == "modified" {
		t.Error("Clone() did not create independent copy")
	}

	cloned.Metadata.Tags["key"] = "changed"
	if original.Metadata.Tags["key"] == "changed" {
		t.Error("Clone() did not deep copy Metadata.Tags")
	}
}

func TestMessageToLLMMessage(t *testing.T) {
	msg := &Message{
		ID: "msg-1",
		Message: llm.Message{
			Role:    llm.RoleAssistant,
			Content: "response",
		},
		Metadata:  MessageMetadata{Model: "claude-3"},
		CreatedAt: time.Now(),
	}

	llmMsg := msg.ToLLMMessage()

	if llmMsg.Role != llm.RoleAssistant {
		t.Errorf("ToLLMMessage().Role = %q, want %q", llmMsg.Role, llm.RoleAssistant)
	}
	if llmMsg.Content != "response" {
		t.Errorf("ToLLMMessage().Content = %q, want %q", llmMsg.Content, "response")
	}

	// 验证提取的是协议层消息（不含业务元数据）
	llmMsg.Content = "changed"
	if msg.Content == "changed" {
		t.Error("ToLLMMessage() should return value, not reference")
	}
}

func TestMessageRoleConstants(t *testing.T) {
	// 验证别名正确映射
	tests := []struct {
		coreRole MessageRole
		llmRole  llm.Role
	}{
		{RoleSystem, llm.RoleSystem},
		{RoleUser, llm.RoleUser},
		{RoleAssistant, llm.RoleAssistant},
		{RoleTool, llm.RoleTool},
	}

	for _, tt := range tests {
		if tt.coreRole != tt.llmRole {
			t.Errorf("Role constant mismatch: %q != %q", tt.coreRole, tt.llmRole)
		}
	}
}

func TestMessageWithToolCalls(t *testing.T) {
	msg := NewTextMessage("msg-1", RoleAssistant, "调用工具")
	msg.ToolCalls = []ToolCall{
		{
			ID:        "call-1",
			Name:      "calculator",
			Arguments: json.RawMessage(`{"expr":"1+1"}`),
		},
	}

	if len(msg.ToolCalls) != 1 {
		t.Errorf("len(ToolCalls) = %d, want 1", len(msg.ToolCalls))
	}
	if msg.ToolCalls[0].Name != "calculator" {
		t.Errorf("ToolCalls[0].Name = %q, want %q", msg.ToolCalls[0].Name, "calculator")
	}
}

func TestMessageMetadata(t *testing.T) {
	msg := NewTextMessage("msg-1", RoleAssistant, "test")
	msg.Metadata = MessageMetadata{
		TokenCount:   42,
		FinishReason: "stop",
		Model:        "claude-sonnet-4",
		Tags: map[string]string{
			"source":   "api",
			"priority": "high",
		},
	}

	if msg.Metadata.TokenCount != 42 {
		t.Errorf("Metadata.TokenCount = %d, want 42", msg.Metadata.TokenCount)
	}
	if msg.Metadata.Model != "claude-sonnet-4" {
		t.Errorf("Metadata.Model = %q, want %q", msg.Metadata.Model, "claude-sonnet-4")
	}
	if msg.Metadata.Tags["source"] != "api" {
		t.Errorf("Metadata.Tags[source] = %q, want %q", msg.Metadata.Tags["source"], "api")
	}
}
