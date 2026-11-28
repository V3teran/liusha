package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

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
		ToolCalls: []llm.ToolCall{{ID: callID, Name: "command_run", Arguments: []byte(`{}`)}},
	}
	tool := llm.Message{Role: llm.RoleTool, ToolCallID: callID, Content: content}
	return assistant, tool
}

// assertNoOrphanTool 验证消息序列中每条 tool 消息的 tool_call_id 都能回指
// 前面的 assistant.tool_calls——这是各协议共同的不变式。
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
	comp := NewContextCompactor(1<<20, "planner", nil, testDistillTimeout)
	msgs := toolPairHistory(3)

	out, err := comp.Modify(context.Background(), msgs)
	require.NoError(t, err)
	assert.Len(t, out, len(msgs))
}

// TestContextCompactor_NeverSplitsToolGroup 验证压缩不得把 assistant(tool_calls)
// ↔ tool 组从中间切开，且压缩后首条非 system 消息是 user。
func TestContextCompactor_NeverSplitsToolGroup(t *testing.T) {
	summaryCache = newSummaryLRU(128)
	comp := NewContextCompactor(24, "planner", nil, testDistillTimeout) // 尾部预算 12：朴素回退切点会落在组内

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
	comp := NewContextCompactor(24, "executor", d, testDistillTimeout)

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
	comp := NewContextCompactor(24, "executor", d, testDistillTimeout)

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
	comp := NewContextCompactor(24, "executor", d, testDistillTimeout)

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

	comp := NewContextCompactor(24, "executor", d, testDistillTimeout)
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
	comp := NewContextCompactor(6, "planner", nil, testDistillTimeout) // 尾部预算 3：装不下任何一条

	out, err := comp.Modify(context.Background(), toolPairHistory(3))
	require.NoError(t, err)
	assertNoOrphanTool(t, out)
	// 尾部至少保住了最后一个完整组（assistant + tool）
	require.True(t, len(out) >= 4, "必须保住尾组而非吐空窗口")
}

// 单条工具结果的窗口：没超过原样进入上下文；超过时留开头与结尾、中间换成说明省掉多少字节的
// 一句话，整条结果（连说明在内）不超过窗口。
func TestWindowToolOutput(t *testing.T) {
	assert.Equal(t, "short", windowToolOutput("short", 100))
	assert.Equal(t, "short", windowToolOutput("short", 0), "0=不设窗口（仅测试）")
	assert.Equal(t, "0123456789", windowToolOutput("0123456789", 10), "恰好等于窗口：原样")

	out := strings.Repeat("a", 1000)
	got := windowToolOutput(out, 200)
	assert.LessOrEqual(t, len(got), 200, "连说明在内不超过窗口")
	head, rest, found := strings.Cut(got, "…[中间省略 ")
	require.True(t, found, "中间应有说明: %q", got)
	digits, tail, found := strings.Cut(rest, " 字节]…")
	require.True(t, found)
	omitted, err := strconv.Atoi(digits)
	require.NoError(t, err)
	assert.Equal(t, 1000-len(head)-len(tail), omitted, "说明里的省略量对得上头尾之外的部分")
	assert.Equal(t, 84, len(head), "头尾各半")
	assert.Equal(t, 84, len(tail))

	// 窗口小到连说明都放不下（只有测试才会这样）：只留结尾，长度不超过窗口。
	got = windowToolOutput(out, 10)
	assert.Equal(t, strings.Repeat("a", 10), got)
}

// 顶格的窗口（单条消息的硬上限）之下，一条远超窗口的结果过了窗口也装得进长度哨兵：哨兵不会拦下
// 任何过了窗口的结果。没给窗口或给得超过硬上限时按硬上限，配置写错也撞不上哨兵。
func TestWindowToolOutput_FitsTheLengthSentinel(t *testing.T) {
	out := strings.Repeat("a", MaxMessageBytes*3)
	sentinel := NewValidateModifier(0)
	for _, window := range []int{MaxMessageBytes, 0, MaxMessageBytes * 2} {
		got := windowToolOutput(out, outputWindow(&ReActConfig{ToolOutputWindowBytes: window}))
		_, err := sentinel.Modify(context.Background(), []llm.Message{{Role: llm.RoleTool, Content: got}})
		require.NoErrorf(t, err, "窗口 %d", window)
	}
}

// 两处截口都对齐 UTF-8 边界：无论怎么切都不落在多字节字符的中间。
func TestWindowToolOutput_AlignsToRuneBoundaries(t *testing.T) {
	out := strings.Repeat("中文", 100) // 600 字节，每字 3 字节
	for _, window := range []int{101, 102, 103, 104, 205} {
		got := windowToolOutput(out, window)
		assert.Truef(t, utf8.ValidString(got), "窗口 %d: 结果应是合法 UTF-8", window)
		assert.Truef(t, strings.HasPrefix(got, "中文"), "窗口 %d: 开头是完整的字", window)
		assert.Truef(t, strings.HasSuffix(got, "中文"), "窗口 %d: 结尾是完整的字", window)
		assert.LessOrEqualf(t, len(got), window, "窗口 %d", window)
	}
}

// 一步里的工具结果合计至多相当于四条顶格的结果：装得下就只是各过单条窗口；装不下时小结果
// 原样保留，大结果均分额度，顺序不变。
func TestFitStepOutputs(t *testing.T) {
	small := strings.Repeat("s", 50)
	big := strings.Repeat("b", 3000)

	// 合计没超：只有超过单条窗口的被收
	got := fitStepOutputs([]string{strings.Repeat("x", 9000), small}, 1000)
	assert.Equal(t, small, got[1])
	assert.LessOrEqual(t, len(got[0]), 1000)
	assert.Contains(t, got[0], "中间省略")

	// 两条大结果加一条小的：预算 4000，小的留 50，余下 3950 由两条均分
	got = fitStepOutputs([]string{big, small, big}, 1000)
	assert.Equal(t, small, got[1], "小结果原样")
	for _, i := range []int{0, 2} {
		assert.LessOrEqual(t, len(got[i]), 1975, "大结果各至多一个均分额度")
		assert.Contains(t, got[i], "中间省略")
		assert.True(t, strings.HasPrefix(got[i], "bbbb"), "大结果仍从头开始")
		assert.True(t, strings.HasSuffix(got[i], "bbbb"), "大结果仍到结尾")
	}

	// 不设窗口（仅测试）：原样
	assert.Equal(t, []string{big}, fitStepOutputs([]string{big}, 0))
}

// 端到端：模型一步发起五个工具调用，各交出 3000 字节；窗口 1000 时合计 5000 超过四个窗口，
// 每条被均分到 800 字节——回传给 provider 的工具消息、轨迹与观察回调看到的都是这一份。
func TestReActRuntime_ToolOutputsFitOneStep(t *testing.T) {
	rt := NewReActRuntime()
	require.NoError(t, rt.RegisterTool(&mockTool{
		name:    "bulk",
		handler: func(json.RawMessage) (any, error) { return strings.Repeat("a", 3000), nil },
	}))
	var calls []llm.ToolCall
	for i := range 5 {
		calls = append(calls, llm.ToolCall{ID: fmt.Sprintf("call_%d", i), Name: "bulk", Arguments: json.RawMessage(`{}`)})
	}
	provider := &recordingProvider{mockLLMProvider: mockLLMProvider{responses: []llm.Response{
		{ToolCalls: calls, FinishReason: "tool_use"},
		{Content: "完成", FinishReason: "stop"},
	}}}
	var seen []string
	cfg := defaultTestConfig()
	cfg.Objective = "取一批数据"
	cfg.LLMProvider = provider
	cfg.ToolOutputWindowBytes = 1000
	cfg.OnObservation = func(o string) { seen = append(seen, o) }
	result, err := rt.Run(context.Background(), cfg)
	require.NoError(t, err)
	assert.Equal(t, ReActStatusSuccess, result.Status)

	require.Len(t, seen, 5)
	for _, o := range seen {
		assert.LessOrEqual(t, len(o), 800, "五条均分四个窗口")
		assert.Contains(t, o, "中间省略")
	}
	require.GreaterOrEqual(t, len(provider.requests), 2)
	var toolMessages []llm.Message
	for _, m := range provider.requests[1].Messages {
		if m.Role == llm.RoleTool {
			toolMessages = append(toolMessages, m)
		}
	}
	require.Len(t, toolMessages, 5)
	for i, m := range toolMessages {
		assert.Equal(t, seen[i], m.Content, "回传给 provider 的就是观察回调看到的那一份")
	}
	assert.Equal(t, seen, result.Trace[0].Observations)
}
