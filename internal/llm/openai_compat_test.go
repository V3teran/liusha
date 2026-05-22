package llm

import (
	"strings"
	"testing"
)

func TestStripImagesToText_TextOnly(t *testing.T) {
	parts := []ContentPart{
		{Type: "text", Text: "hello"},
		{Type: "text", Text: "world"},
	}
	got := stripImagesToText(parts)
	want := "hello\nworld"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestStripImagesToText_WithImages(t *testing.T) {
	parts := []ContentPart{
		{Type: "text", Text: "screenshot returned:"},
		{Type: "image_url", ImageURL: &ImageContent{MediaType: "image/png", Base64Data: "iVBOR..."}},
		{Type: "text", Text: "url=http://x"},
	}
	got := stripImagesToText(parts)
	if !strings.Contains(got, "screenshot returned:") {
		t.Errorf("应保留 text 部分，got=%q", got)
	}
	if !strings.Contains(got, "[Image removed") {
		t.Errorf("image_url 应换占位文本，got=%q", got)
	}
	if !strings.Contains(got, "url=http://x") {
		t.Errorf("应保留后续 text，got=%q", got)
	}
}

func TestStripImagesToText_EmptyTextSkipped(t *testing.T) {
	parts := []ContentPart{
		{Type: "text", Text: ""},
		{Type: "image_url", ImageURL: &ImageContent{MediaType: "image/png", Base64Data: "x"}},
		{Type: "text", Text: "after"},
	}
	got := stripImagesToText(parts)
	// 空 text 跳过，不应出现 leading "\n"
	if strings.HasPrefix(got, "\n") {
		t.Errorf("空 text 应跳过不留 leading \\n，got=%q", got)
	}
}

func TestToOpenAIMessages_StripsImagesGracefully(t *testing.T) {
	msgs := []Message{
		{Role: RoleTool, ToolCallID: "call_1", ContentParts: []ContentPart{
			{Type: "text", Text: "tool output"},
			{Type: "image_url", ImageURL: &ImageContent{MediaType: "image/png", Base64Data: "iVBOR"}},
		}},
	}
	out, err := toOpenAIMessages(msgs, false) // 测降级路径：supportsVision=false → 走 stripImagesToText
	if err != nil {
		t.Fatalf("应 graceful degrade 不报错，实际 err=%v", err)
	}
	if len(out) != 1 {
		t.Fatalf("期望 1 条 message，实际 %d", len(out))
	}
	if !strings.Contains(out[0].Content, "tool output") {
		t.Errorf("应保留 tool 文本，content=%q", out[0].Content)
	}
	if !strings.Contains(out[0].Content, "[Image removed") {
		t.Errorf("应有图片占位，content=%q", out[0].Content)
	}
	if out[0].ToolCallID != "call_1" {
		t.Errorf("ToolCallID 应保留，got=%q", out[0].ToolCallID)
	}
}
