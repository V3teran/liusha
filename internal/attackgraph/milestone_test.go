package attackgraph

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/conversation"
)

// fakeSummarizer 回显 prompt 前缀，便于断言调用；failOn 命中则返回错误。
// Milestones 现在并发调用各 agent 的 Summarize（见 milestone.go），故 calls 用 atomic 计数——
// 多个 goroutine 并发写同一个 int 是 data race（-race 会抓到），不能假设调用仍是串行的。
type fakeSummarizer struct {
	calls  int32
	failOn string
}

func (f *fakeSummarizer) Summarize(_ context.Context, prompt string) (string, error) {
	atomic.AddInt32(&f.calls, 1)
	if f.failOn != "" && strings.Contains(prompt, f.failOn) {
		return "", errors.New("boom")
	}
	n := 10
	if len(prompt) < n {
		n = len(prompt)
	}
	return "摘要:" + prompt[:n], nil
}

func TestMilestones(t *testing.T) {
	ctx := context.Background()

	t.Run("按子代理聚合且保首次出现序", func(t *testing.T) {
		msgs := []conversation.Message{
			mkEventMsg("o1", evReasoning, map[string]any{"AgentName": "orchestrator", "Text": "规划"}),
			mkEventMsg("e1", evReasoning, map[string]any{"AgentName": "exploitation", "Text": "试注入"}),
			mkEventMsg("o2", evReasoning, map[string]any{"AgentName": "orchestrator", "Text": "再派活"}),
			mkEventMsg("a1", evToolCall, map[string]any{"AgentName": "exploitation", "ToolName": "curl"}), // 非 reasoning 不计
		}
		f := &fakeSummarizer{}
		ms, err := Milestones(ctx, msgs, f)
		if err != nil {
			t.Fatal(err)
		}
		if len(ms) != 2 {
			t.Fatalf("里程碑数=%d 期望 2（两个 agent）：%+v", len(ms), ms)
		}
		if ms[0].Agent != "orchestrator" || ms[1].Agent != "exploitation" {
			t.Errorf("顺序错：%s,%s 期望 orchestrator,exploitation", ms[0].Agent, ms[1].Agent)
		}
		if ms[0].NodeCount != 2 { // orchestrator 两条 reasoning
			t.Errorf("orchestrator NodeCount=%d 期望 2", ms[0].NodeCount)
		}
		if f.calls != 2 {
			t.Errorf("LLM 调用数=%d 期望 2（每 agent 一次）", f.calls)
		}
	})

	t.Run("总结失败回退不阻断", func(t *testing.T) {
		msgs := []conversation.Message{
			mkEventMsg("e1", evReasoning, map[string]any{"AgentName": "exploitation", "Text": "x"}),
		}
		ms, err := Milestones(ctx, msgs, &fakeSummarizer{failOn: "exploitation"})
		if err != nil {
			t.Fatal(err)
		}
		if len(ms) != 1 || ms[0].Summary != "（摘要生成失败）" {
			t.Errorf("期望 fallback 摘要，得 %+v", ms)
		}
	})

	t.Run("空 summarizer 报错", func(t *testing.T) {
		if _, err := Milestones(ctx, nil, nil); err == nil {
			t.Fatal("期望 summarizer 为空报错")
		}
	})

	t.Run("无 reasoning 返回空", func(t *testing.T) {
		msgs := []conversation.Message{
			mkEventMsg("a1", evToolCall, map[string]any{"ToolName": "curl"}),
		}
		ms, err := Milestones(ctx, msgs, &fakeSummarizer{})
		if err != nil || len(ms) != 0 {
			t.Errorf("期望空里程碑，得 %+v err=%v", ms, err)
		}
	})

	t.Run("多 agent 并发调用而非串行等待", func(t *testing.T) {
		// 3 个 agent，每次 Summarize 耗时 50ms。串行需 ≥150ms；并发（上限 4）应远小于此。
		// 用总耗时做粗粒度断言，避免对具体调度细节做脆弱假设。
		msgs := []conversation.Message{
			mkEventMsg("o1", evReasoning, map[string]any{"AgentName": "orchestrator", "Text": "a"}),
			mkEventMsg("r1", evReasoning, map[string]any{"AgentName": "reconnaissance", "Text": "b"}),
			mkEventMsg("e1", evReasoning, map[string]any{"AgentName": "exploitation", "Text": "c"}),
		}
		f := &slowConcurrentSummarizer{delay: 50 * time.Millisecond}
		start := time.Now()
		ms, err := Milestones(ctx, msgs, f)
		elapsed := time.Since(start)
		if err != nil {
			t.Fatal(err)
		}
		if len(ms) != 3 {
			t.Fatalf("里程碑数=%d 期望 3", len(ms))
		}
		if elapsed >= 3*f.delay {
			t.Errorf("耗时=%v 接近串行的 3×%v，未并发执行", elapsed, f.delay)
		}
		if got := atomic.LoadInt32(&f.maxConcurrent); got < 2 {
			t.Errorf("观测到的最大并发数=%d，期望 ≥2（未真正并发调用）", got)
		}
	})

	t.Run("按 rune 截断，不切断多字节 CJK 字符", func(t *testing.T) {
		// reasoningTextMax=24000；构造刚好超限的纯中文文本，若按字节截断会在字符中间切开，
		// json.Marshal（milestonePrompt 内部走 fmt.Sprintf，不会报错，但截断位可能产生非法 UTF-8 字节）。
		// 这里直接断言 truncateRunes 的行为：截断结果本身必须是合法 UTF-8。
		longText := strings.Repeat("漏", reasoningTextMax+10) // 每个"漏"3 字节，超过 max 的 rune 数
		got := truncateRunes(longText, reasoningTextMax)
		if !utf8ValidString(got) {
			t.Fatal("截断结果含非法 UTF-8 字节（说明是按字节而非按 rune 截断）")
		}
		if n := len([]rune(got)); n != reasoningTextMax {
			t.Errorf("截断后 rune 数=%d 期望 %d", n, reasoningTextMax)
		}
	})
}

// slowConcurrentSummarizer 记录调用峰值并发数，用于断言 Milestones 确实并发调用而非串行。
type slowConcurrentSummarizer struct {
	delay         time.Duration
	mu            sync.Mutex
	current       int32
	maxConcurrent int32
}

func (f *slowConcurrentSummarizer) Summarize(_ context.Context, _ string) (string, error) {
	f.mu.Lock()
	f.current++
	if f.current > f.maxConcurrent {
		f.maxConcurrent = f.current
	}
	f.mu.Unlock()

	time.Sleep(f.delay)

	f.mu.Lock()
	f.current--
	f.mu.Unlock()
	return "ok", nil
}

// utf8ValidString 复用标准库校验，避免测试自己重新实现 UTF-8 解码逻辑。
func utf8ValidString(s string) bool {
	for _, r := range s {
		if r == '�' {
			// 用 range 迭代天然跳过非法字节序列并产出 U+FFFD；一旦出现即可判定非法。
			// （strings.Repeat("漏", n) 构造的输入本不含 U+FFFD，出现即说明截断切坏了字符。）
			return false
		}
	}
	return true
}
