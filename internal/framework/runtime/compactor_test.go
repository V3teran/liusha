package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/V3teran/liusha/internal/framework/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mkMsg 构造单条消息。
func mkMsg(role llm.MessageRole, content string) llm.Message {
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

// stubDistiller 是可编程的蒸馏器测试替身：记录每次调用，按脚本返回。
type stubDistiller struct {
	calls int
	// got 记录每次蒸馏的入参（角色/被裁前缀/旧纪要）。
	roles    []string
	priores  []string
	evicteds [][]llm.Message
	script   []struct {
		out string
		err error
	}
}

func (s *stubDistiller) Distill(_ context.Context, role string, evicted []llm.Message, prior string) (string, error) {
	i := s.calls
	if i < len(s.script) {
		s.calls++
		s.roles = append(s.roles, role)
		s.priores = append(s.priores, prior)
		cp := make([]llm.Message, len(evicted))
		copy(cp, evicted)
		s.evicteds = append(s.evicteds, cp)
		return s.script[i].out, s.script[i].err
	}
	s.calls++
	return "", errors.New("脚本耗尽")
}

// toolPairHistory 构造 system + user + n 组 assistant↔tool 的标准历史。
func toolPairHistory(n int) []llm.Message {
	msgs := []llm.Message{mkMsg(llm.RoleSystem, "system"), mkMsg(llm.RoleUser, "objective")}
	for i := 0; i < n; i++ {
		a, tool := mkToolPair(strings.Repeat("c", i+1), strings.Repeat("observation-", i+1))
		msgs = append(msgs, a, tool)
	}
	return msgs
}

// TestContextCompactor_UnderBudgetPassthrough 预算内不裁剪、不插占位。
func TestContextCompactor_UnderBudgetPassthrough(t *testing.T) {
	comp := NewContextCompactor(1<<20, "planner", nil)
	msgs := toolPairHistory(3)

	out, err := comp.Modify(context.Background(), msgs)
	require.NoError(t, err)
	assert.Len(t, out, len(msgs))
}

// TestContextCompactor_NeverSplitsToolGroup 验证压缩不得把 assistant(tool_calls)
// ↔ tool 组从中间切开，且压缩后首条非 system 消息是 user。
func TestContextCompactor_NeverSplitsToolGroup(t *testing.T) {
	summaryCache = newSummaryLRU(128)
	comp := NewContextCompactor(24, "planner", nil) // 尾部预算 12：朴素回退切点会落在组内

	out, err := comp.Modify(context.Background(), toolPairHistory(4))
	require.NoError(t, err)

	assert.Equal(t, llm.RoleSystem, out[0].Role, "system 消息必须保留在最前")
	assertNoOrphanTool(t, out)
	require.Equal(t, llm.RoleUser, out[1].Role, "压缩后首条非 system 必须是 user（占位/纪要）")
	require.Equal(t, truncationNotice, out[1].Content, "无蒸馏器时降级为占位声明")
}

// TestContextCompactor_DistillsEvictedPrefix 蒸馏器在场时：被裁前缀交给蒸馏器、
// 纪要以 SummaryPrefix 前缀顶替占位声明。
func TestContextCompactor_DistillsEvictedPrefix(t *testing.T) {
	summaryCache = newSummaryLRU(128)
	d := &stubDistiller{script: []struct {
		out string
		err error
	}{{out: "进展：完成了 3 组工具调用", err: nil}}}
	comp := NewContextCompactor(24, "executor", d)

	out, err := comp.Modify(context.Background(), toolPairHistory(4))
	require.NoError(t, err)

	require.Equal(t, 1, d.calls)
	assert.Equal(t, "executor", d.roles[0])
	require.NotEmpty(t, d.evicteds[0], "被裁前缀必须传给蒸馏器")
	require.Equal(t, SummaryPrefix+"\n进展：完成了 3 组工具调用", out[1].Content)
	// 纪要之后是完整保留的尾部
	assertNoOrphanTool(t, out)
}

// TestContextCompactor_DistillFailureFallsBack 蒸馏失败降级占位声明，不返回错误。
func TestContextCompactor_DistillFailureFallsBack(t *testing.T) {
	summaryCache = newSummaryLRU(128)
	d := &stubDistiller{script: []struct {
		out string
		err error
	}{{out: "", err: errors.New("provider 不可用")}}}
	comp := NewContextCompactor(24, "executor", d)

	out, err := comp.Modify(context.Background(), toolPairHistory(4))
	require.NoError(t, err, "蒸馏失败绝不能弄死压缩")
	assert.Equal(t, truncationNotice, out[1].Content)
}

// TestContextCompactor_FoldsPriorSummary 连续压缩时旧纪要随前缀被裁，
// 抽出折叠传给蒸馏器（prior 语义）。
func TestContextCompactor_FoldsPriorSummary(t *testing.T) {
	summaryCache = newSummaryLRU(128)
	d := &stubDistiller{script: []struct {
		out string
		err error
	}{{out: "折叠后的新纪要", err: nil}}}
	comp := NewContextCompactor(24, "executor", d)

	msgs := append([]llm.Message{
		mkMsg(llm.RoleSystem, "system"),
		mkMsg(llm.RoleUser, SummaryPrefix+"\n上一轮的旧纪要"),
	}, toolPairHistory(4)[1:]...)

	out, err := comp.Modify(context.Background(), msgs)
	require.NoError(t, err)
	require.Equal(t, 1, d.calls)
	assert.Equal(t, "上一轮的旧纪要", d.priores[0], "旧纪要必须折叠传给蒸馏器")
	assert.Equal(t, SummaryPrefix+"\n折叠后的新纪要", out[1].Content)
}

// TestContextCompactor_CachesSummary 同一被裁前缀的重复压缩命中缓存，不再烧蒸馏。
func TestContextCompactor_CachesSummary(t *testing.T) {
	summaryCache = newSummaryLRU(128)
	d := &stubDistiller{script: []struct {
		out string
		err error
	}{{out: "唯一纪要", err: nil}}}
	msgs := toolPairHistory(4)

	comp := NewContextCompactor(24, "executor", d)
	_, err := comp.Modify(context.Background(), msgs)
	require.NoError(t, err)
	_, err = comp.Modify(context.Background(), msgs) // 重试同一历史
	require.NoError(t, err)

	assert.Equal(t, 1, d.calls, "同前缀第二次压缩必须命中缓存")
}

// TestContextCompactor_TailGroupFallback 预算小到连最后一个完整组都装不下时
// 保尾组直通（空窗口没有收益，悬空 tool 结果会被拒收）。
func TestContextCompactor_TailGroupFallback(t *testing.T) {
	summaryCache = newSummaryLRU(128)
	comp := NewContextCompactor(6, "planner", nil) // 尾部预算 3：装不下任何一条

	out, err := comp.Modify(context.Background(), toolPairHistory(3))
	require.NoError(t, err)
	assertNoOrphanTool(t, out)
	// 尾部至少保住了最后一个完整组（assistant + tool）
	require.True(t, len(out) >= 4, "必须保住尾组而非吐空窗口")
}

// TestClampTail 工具输出全局钳制：尾部保留 + UTF-8 边界对齐 + 阈内直通。
func TestClampTail(t *testing.T) {
	assert.Equal(t, "short", clampTail("short", 100))
	assert.Equal(t, "short", clampTail("short", 0), "0=不钳（仅测试）")

	// 超限：尾部保留 + 前缀标记
	got := clampTail("0123456789", 4)
	assert.True(t, strings.HasSuffix(got, "6789"))
	assert.True(t, strings.HasPrefix(got, "...[前段已截断]"))

	// UTF-8 边界：截口落在多字节 rune 中间时丢弃残字节（"中文中文中文" 每字
	// 3 字节，取尾部 7 字节的裁口落在第 4 个汉字的末字节——残字节被丢弃后
	// 只剩最后两个完整汉字）。
	s := "中文中文中文"
	got = clampTail(s, 7)
	assert.True(t, strings.HasSuffix(got, "中文"),
		"截口必须落在 rune 边界（保留完整 rune）")
	assert.LessOrEqual(t, len(got), len("...[前段已截断]\n")+7)
}

// TestCallLLM_CompactionCheckpoint 验证压缩的检查点语义：压缩结果写回权威
// 历史——二次压缩时旧纪要随被裁前缀进入蒸馏器折叠。若压缩只是视图层变换
// （历史原样保留），每次调用都要重新压缩同一历史且纪要永不在场，折叠链路
// 整体失效——本测试守住写回这一接线。
func TestCallLLM_CompactionCheckpoint(t *testing.T) {
	summaryCache = newSummaryLRU(128)
	d := &stubDistiller{script: []struct {
		out string
		err error
	}{{out: "纪要一", err: nil}, {out: "纪要二（已折叠纪要一）", err: nil}}}
	provider := &mockLLMProvider{responses: []llm.Response{{Content: "done"}}}
	r := NewReActRuntime().(*reactRuntime)
	config := &ReActConfig{
		LLMProvider:          provider,
		MessageModifierChain: NewCompactionChain(24, "test", d),
	}

	countSummaries := func(msgs []llm.Message) int {
		n := 0
		for _, m := range msgs {
			if strings.HasPrefix(m.Content, SummaryPrefix) {
				n++
			}
		}
		return n
	}

	result := &ReActResult{MessageHistory: toolPairHistory(4)}
	_, err := r.callLLM(context.Background(), config, result)
	require.NoError(t, err)
	require.Equal(t, 1, d.calls)
	require.Equal(t, llm.RoleSystem, result.MessageHistory[0].Role, "写回后权威历史以 system 开头")
	require.Equal(t, 1, countSummaries(result.MessageHistory), "写回后权威历史含一份纪要")

	// 追加新一轮工具往返（历史重新逼近预算）后二次调用：新前缀新蒸馏，
	// 旧纪要折叠进新纪要，权威历史仍只有一份纪要。
	result.AddAssistantMessage("", []llm.ToolCall{{ID: "zz", Name: "run_command", Arguments: []byte(`{}`)}}, nil)
	result.AddToolMessage("zz", strings.Repeat("observation-", 4))
	_, err = r.callLLM(context.Background(), config, result)
	require.NoError(t, err)
	require.Equal(t, 2, d.calls, "新的被裁前缀必须新蒸馏")
	assert.Equal(t, "纪要一", d.priores[1], "旧纪要必须折叠传给蒸馏器")
	assert.Equal(t, 1, countSummaries(result.MessageHistory), "折叠后不得残留两份纪要")
}

// TestContextCompactor_AmortizationGap 摊销回归：压缩后总量必须显著低于预算
// （≤80%）。检查点语义的经济性前提是「压缩后到下次触发之间有追加余量」——
// 贴线压缩会让每条新消息立即再度触发压缩与蒸馏，摊销归零（本测试守住
// 尾部预算公式里的落差系数）。
func TestContextCompactor_AmortizationGap(t *testing.T) {
	summaryCache = newSummaryLRU(128)
	budget := 400
	comp := NewContextCompactor(budget, "planner", nil)

	// system 极小 + 大量等长小消息（每对 ~5 token）：总量远超预算，且单条
	// 消息相对预算可忽略（摊销断言只在无单条支配时成立——toolPairHistory
	// 的后段 pair 递增增长会被「至少保留最后一条」规则支配，不可用）。
	msgs := []llm.Message{mkMsg(llm.RoleSystem, "system"), mkMsg(llm.RoleUser, "objective")}
	for i := 0; i < 90; i++ {
		a, tool := mkToolPair(fmt.Sprintf("c%d", i), "observation")
		msgs = append(msgs, a, tool)
	}
	out, err := comp.Modify(context.Background(), msgs)
	require.NoError(t, err)

	total := llm.EstimateMessagesTokens(out)
	assert.LessOrEqual(t, total, budget*8/10,
		"压缩后总量 %d 贴近/超出预算 %d——摊销落差丢失，检查点退化为每步压缩", total, budget)
	assert.Less(t, total, llm.EstimateMessagesTokens(msgs), "必须实际发生压缩")
}

// TestCompleteForced_ConvergesHistory 强制收尾是唯一绕过 callLLM 的出站点，
// 不得成为预算治理的旁路：收尾调用前必须过修改链并写回。
func TestCompleteForced_ConvergesHistory(t *testing.T) {
	summaryCache = newSummaryLRU(128)
	d := &stubDistiller{script: []struct {
		out string
		err error
	}{{out: "收尾前纪要", err: nil}}}
	provider := &mockLLMProvider{responses: []llm.Response{{Content: "最终总结"}}}
	r := NewReActRuntime().(*reactRuntime)
	config := &ReActConfig{
		LLMProvider:          provider,
		MaxTokens:            1000,
		MessageModifierChain: NewCompactionChain(24, "test", d),
	}

	result := &ReActResult{MessageHistory: toolPairHistory(4)}
	_, err := r.completeForced(context.Background(), config, result, "输出最终总结")
	require.NoError(t, err)

	require.Equal(t, 1, d.calls, "收尾调用必须先触发压缩")
	assert.Equal(t, llm.RoleSystem, result.MessageHistory[0].Role)
	found := false
	for _, m := range result.MessageHistory {
		if strings.HasPrefix(m.Content, SummaryPrefix) {
			found = true
		}
	}
	assert.True(t, found, "收尾历史必须含收敛纪要")
}
