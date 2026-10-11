package runtime

import (
	"context"
	"testing"

	"github.com/V3teran/liusha/internal/framework/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mkMsg 构造单条消息。
func mkMsg(role llm.Role, content string) llm.Message {
	return llm.Message{Role: role, Content: content}
}

// mkToolPair 构造一对 assistant(tool_calls) ↔ tool 消息（协议不可再分的组）。
func mkToolPair(callID, content string) (llm.Message, llm.Message) {
	assistant := llm.Message{
		Role:      llm.RoleAssistant,
		ToolCalls: []llm.ToolCall{{ID: callID, Name: "run_command", Arguments: []byte(`{}`)}},
	}
	tool := llm.Message{Role: llm.RoleTool, ToolCallID: callID, Content: content}
	return assistant, tool
}

// assertNoOrphanTool 验证消息序列中每条 tool 消息的 tool_call_id 都能回指
// 前面的 assistant.tool_calls——这是 openai-compat / anthropic 共同的协议不变式。
func assertNoOrphanTool(t *testing.T, msgs []llm.Message) {
	t.Helper()
	seen := map[string]bool{}
	for _, m := range msgs {
		for _, tc := range m.ToolCalls {
			seen[tc.ID] = true
		}
		if m.Role == llm.RoleTool {
			require.True(t, seen[m.ToolCallID],
				"tool 消息（tool_call_id=%s）的父 assistant 已被切掉——悬空 tool 结果", m.ToolCallID)
		}
	}
}

// TestRollingWindow_NeverSplitsToolGroup 验证窗口压缩不得把
// assistant(tool_calls) ↔ tool 组从中间切开（切点落在组内时整组丢弃）。
func TestRollingWindow_NeverSplitsToolGroup(t *testing.T) {
	mod := NewRollingWindowModifier(5)

	// 构造：system + user + 4 组 assistant↔tool 对
	msgs := []llm.Message{mkMsg(llm.RoleSystem, "system")}
	msgs = append(msgs, mkMsg(llm.RoleUser, "objective"))
	for i, id := range []string{"c1", "c2", "c3", "c4"} {
		a, tool := mkToolPair(id, "observation-"+string(rune('a'+i)))
		msgs = append(msgs, a, tool)
	}
	// 非系统消息共 9 条 > 窗口 5：朴素切法（保留末 5 条）起点是 c2 组的 tool 消息——悬空。
	out, err := mod.Modify(context.Background(), msgs)
	require.NoError(t, err)

	assert.Equal(t, llm.RoleSystem, out[0].Role, "system 消息必须保留在窗口前")
	assertNoOrphanTool(t, out)

	// 边界对齐后：占位 user 在前，随后是 c3 组的 assistant（c2 组因被切中而整组丢弃）
	require.Equal(t, llm.RoleUser, out[1].Role, "压缩后应补占位 user")
	first := out[2]
	require.Equal(t, llm.RoleAssistant, first.Role, "窗口起点不得是 tool 消息")
	require.NotEmpty(t, first.ToolCalls)
	assert.Equal(t, "c3", first.ToolCalls[0].ID)
}

// TestRollingWindow_BoundaryOnGroupStart 验证切点恰好在组边界时不多丢。
func TestRollingWindow_BoundaryOnGroupStart(t *testing.T) {
	mod := NewRollingWindowModifier(4)

	msgs := []llm.Message{mkMsg(llm.RoleSystem, "system")}
	msgs = append(msgs, mkMsg(llm.RoleUser, "objective"))
	for _, id := range []string{"c1", "c2", "c3"} {
		a, tool := mkToolPair(id, "obs-"+id)
		msgs = append(msgs, a, tool)
	}
	// 非系统 7 条 > 4：cut=3 落在 c2 组的 assistant 上（组边界），组完整性无需对齐；
	// 但起点非 user → 仍补占位 user（协议要求首条非 system 是 user）。
	out, err := mod.Modify(context.Background(), msgs)
	require.NoError(t, err)
	assertNoOrphanTool(t, out)
	require.Len(t, out, 6) // system + 占位 user + 窗口 4 条
	assert.Equal(t, "c2", out[2].ToolCalls[0].ID)
}

// TestRollingWindow_NoTruncationNeeded 验证窗口内不裁剪。
func TestRollingWindow_NoTruncationNeeded(t *testing.T) {
	mod := NewRollingWindowModifier(10)
	msgs := []llm.Message{mkMsg(llm.RoleSystem, "s"), mkMsg(llm.RoleUser, "u")}
	a, tool := mkToolPair("c1", "obs")
	msgs = append(msgs, a, tool)

	out, err := mod.Modify(context.Background(), msgs)
	require.NoError(t, err)
	assert.Len(t, out, 3+1) // system + user + assistant + tool
}
