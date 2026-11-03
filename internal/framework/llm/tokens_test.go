package llm

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestEstimateTokens 口径：非 ASCII 按 1.5 token/字向上取整（分词器密度
// 谱系上侧，估算是溢出保护的唯一事实源），ASCII 每 3 字符 1 token。
func TestEstimateTokens(t *testing.T) {
	assert.Zero(t, EstimateTokens(""))
	assert.Equal(t, 8, EstimateTokens("上下文压缩"), "5 CJK → ceil(7.5)=8")
	assert.Equal(t, 2, EstimateTokens("中"), "1 CJK → ceil(1.5)=2")
	assert.Equal(t, 4, EstimateTokens("abcdefghij"), "10 ASCII → ceil(10/3)=4")
	assert.Equal(t, 3, EstimateTokens("a中"), "混合：1 CJK→2 + 1 ASCII→1")
}

// TestEstimateMessageTokens_ThinkingCounted 思考块随多轮 tool_use 回传、真实
// 消耗上下文——漏计会让预算系统性低估、压缩触发过晚。
func TestEstimateMessageTokens_ThinkingCounted(t *testing.T) {
	base := llmMsg("hello world") // 11 ASCII → 4
	withThinking := Message{Role: RoleAssistant, Content: "hello world", Thinking: &ThinkingBlock{Text: "thinking text here"}}

	assert.Equal(t, 4, EstimateMessageTokens(base))
	assert.Greater(t, EstimateMessageTokens(withThinking), EstimateMessageTokens(base),
		"思考块必须计入估算")
	assert.Equal(t, 4+EstimateTokens("thinking text here"), EstimateMessageTokens(withThinking))
}

// TestEstimateMessageTokens_ToolCallsCounted 工具调用名与参数计入。
func TestEstimateMessageTokens_ToolCallsCounted(t *testing.T) {
	m := Message{Role: RoleAssistant, ToolCalls: []ToolCall{{Name: "run_command", Arguments: []byte(`{"cmd":"ls"}`)}}}
	assert.Greater(t, EstimateMessageTokens(m), 0)
}

func llmMsg(content string) Message { return Message{Role: RoleUser, Content: content} }
