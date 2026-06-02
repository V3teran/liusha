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

// vision provider 下，tool 结果含图：OpenAI 协议禁止 tool role 带 image multipart
// （小米 MiMo 等严格实现报 400 Param Incorrect）→ tool message 仅留文本，图拆到紧随的 user message。
func TestToOpenAIMessages_VisionToolImage_SplitToUserMsg(t *testing.T) {
	msgs := []Message{
		{Role: RoleTool, ToolCallID: "call_1", Name: "browser_use", ContentParts: []ContentPart{
			{Type: "text", Text: "url=http://x"},
			{Type: "image_url", ImageURL: &ImageContent{MediaType: "image/png", Base64Data: "iVBOR"}},
		}},
	}
	out, err := toOpenAIMessages(msgs, true) // supportsVision=true
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if len(out) != 2 {
		t.Fatalf("期望 2 条（tool 文本 + user 图），实际 %d", len(out))
	}
	// [0] tool message：纯文本，无 image multipart，保留 tool_call_id
	if out[0].Role != "tool" {
		t.Errorf("out[0] 应为 tool role，got=%q", out[0].Role)
	}
	if out[0].ToolCallID != "call_1" {
		t.Errorf("tool_call_id 应保留，got=%q", out[0].ToolCallID)
	}
	for _, p := range out[0].MultiContent {
		if p.Type == openaiImageType {
			t.Errorf("tool message 不应含 image（OpenAI 禁止 tool role 带图）")
		}
	}
	if !strings.Contains(out[0].Content, "url=http://x") {
		t.Errorf("tool 文本应保留，got=%q", out[0].Content)
	}
	// [1] user message：带 image_url
	if out[1].Role != "user" {
		t.Errorf("out[1] 应为 user role，got=%q", out[1].Role)
	}
	hasImg := false
	for _, p := range out[1].MultiContent {
		if p.Type == openaiImageType {
			hasImg = true
		}
	}
	if !hasImg {
		t.Errorf("user message 应携带 image_url，got=%+v", out[1].MultiContent)
	}
}

// 并行 tool calls（一个 assistant turn 多 tool）：所有 tool message 必须连续，
// 图片合并到最后一条 user message——不能在 tool message 之间插 user message（OpenAI 协议会拒）。
func TestToOpenAIMessages_ParallelToolImages_NoInterleave(t *testing.T) {
	msgs := []Message{
		{Role: RoleAssistant, Content: "并行调两个工具", ToolCalls: []ToolCall{
			{ID: "c1", Name: "browser_use", Arguments: []byte("{}")},
			{ID: "c2", Name: "browser_use", Arguments: []byte("{}")},
		}},
		{Role: RoleTool, ToolCallID: "c1", ContentParts: []ContentPart{
			{Type: "text", Text: "r1"},
			{Type: "image_url", ImageURL: &ImageContent{MediaType: "image/png", Base64Data: "a"}},
		}},
		{Role: RoleTool, ToolCallID: "c2", ContentParts: []ContentPart{
			{Type: "text", Text: "r2"},
			{Type: "image_url", ImageURL: &ImageContent{MediaType: "image/png", Base64Data: "b"}},
		}},
	}
	out, err := toOpenAIMessages(msgs, true)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	// 期望: assistant, tool(c1), tool(c2), user(2图)
	if len(out) != 4 {
		t.Fatalf("期望 4 条，实际 %d", len(out))
	}
	if out[0].Role != "assistant" || out[1].Role != "tool" || out[2].Role != "tool" || out[3].Role != "user" {
		t.Fatalf("顺序应为 assistant,tool,tool,user，实际 %q,%q,%q,%q",
			out[0].Role, out[1].Role, out[2].Role, out[3].Role)
	}
	imgCount := 0
	for _, p := range out[3].MultiContent {
		if p.Type == openaiImageType {
			imgCount++
		}
	}
	if imgCount != 2 {
		t.Errorf("末尾 user message 应合并 2 张图，got %d", imgCount)
	}
}
