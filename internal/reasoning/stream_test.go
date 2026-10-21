package reasoning

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/conversation"
)

// fakePub 记录全部广播调用（增量帧与终态帧），供断言合批与保序。
type fakePub struct {
	mu     sync.Mutex
	deltas []struct {
		agent, channel, text string
	}
	payloads [][]byte
}

func (f *fakePub) PublishDelta(_ context.Context, _, agent, channel, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deltas = append(f.deltas, struct{ agent, channel, text string }{agent, channel, text})
	return nil
}

func (f *fakePub) Publish(_ context.Context, _ string, payload []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.payloads = append(f.payloads, payload)
	return nil
}

// fakeWriter 记录终态落库调用。
type fakeWriter struct {
	mu       sync.Mutex
	appended []conversation.Message
	metas    []json.RawMessage
}

func (f *fakeWriter) AppendMessage(_ context.Context, _ string, role conversation.Role, kind conversation.Kind, content string, meta json.RawMessage) (conversation.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.appended = append(f.appended, conversation.Message{Role: role, Kind: kind, Content: content})
	f.metas = append(f.metas, meta)
	return conversation.Message{ID: "m1", Role: role, Kind: kind, Content: content, Metadata: meta}, nil
}

func newTestStream(convID string) (*Stream, *fakePub, *fakeWriter) {
	pub, w := &fakePub{}, &fakeWriter{}
	s := NewStream(pub, w, convID, zerolog.Nop())
	return s, pub, w
}

// 合批：同 agent 窗口内的增量合并成一帧；不同 agent 各一帧且保首现序。
func TestStream_FlushCoalescesPerAgent(t *testing.T) {
	s, pub, _ := newTestStream("c1")
	exec := s.TapFor("executor")
	planner := s.TapFor("planner")

	exec.Delta("探")
	exec.Delta("测目标")
	planner.Delta("规划中")

	s.flush()

	if len(pub.deltas) != 2 {
		t.Fatalf("应按 agent 合批为 2 帧, got %d: %+v", len(pub.deltas), pub.deltas)
	}
	if pub.deltas[0].agent != "executor" || pub.deltas[0].text != "探测目标" {
		t.Fatalf("executor 帧 = %+v", pub.deltas[0])
	}
	if pub.deltas[1].agent != "planner" || pub.deltas[1].text != "规划中" {
		t.Fatalf("planner 帧 = %+v", pub.deltas[1])
	}
	s.Close()
}

// Done：先冲刷待发增量，再落 kind=event + metadata{kind:reasoning,agent_name}
// 终态消息并广播；之后该 agent 的新增量进入新一轮。
func TestStream_DoneFlushesAndPersists(t *testing.T) {
	s, pub, w := newTestStream("c1")
	tap := s.TapFor("executor")

	tap.Delta("思考片段")
	tap.Done("本轮结论", "思考终稿")

	if len(pub.deltas) != 1 || pub.deltas[0].text != "思考片段" {
		t.Fatalf("Done 应先冲刷待发增量, got %+v", pub.deltas)
	}
	if len(w.appended) != 1 {
		t.Fatalf("应落一条终态消息, got %d", len(w.appended))
	}
	m := w.appended[0]
	if m.Role != conversation.RoleAssistant || m.Kind != conversation.KindEvent {
		t.Fatalf("终态消息 role/kind = %s/%s", m.Role, m.Kind)
	}
	var meta struct {
		Kind      string `json:"kind"`
		AgentName string `json:"agent_name"`
	}
	if err := json.Unmarshal(w.metas[0], &meta); err != nil {
		t.Fatalf("metadata 解析: %v", err)
	}
	if meta.Kind != "reasoning" || meta.AgentName != "executor" {
		t.Fatalf("metadata = %+v", meta)
	}
	if len(pub.payloads) != 1 {
		t.Fatalf("终态消息应广播, got %d", len(pub.payloads))
	}
	s.Close()
}

// 无会话（convID 空）：全链路 no-op。
func TestStream_DisabledWhenNoConversation(t *testing.T) {
	s, pub, w := newTestStream("")
	tap := s.TapFor("executor")
	tap.Delta("x")
	tap.Done("y", "")
	s.flush()
	s.Close()
	if len(pub.deltas) != 0 || len(pub.payloads) != 0 || len(w.appended) != 0 {
		t.Fatalf("无会话应 no-op, got deltas=%d payloads=%d appended=%d",
			len(pub.deltas), len(pub.payloads), len(w.appended))
	}
}

// Close：冲刷残留增量后停止。
func TestStream_CloseFlushesPending(t *testing.T) {
	s, pub, _ := newTestStream("c1")
	s.TapFor("monitor").Delta("巡查")
	s.Close()
	if len(pub.deltas) != 1 || pub.deltas[0].text != "巡查" {
		t.Fatalf("Close 应冲刷残留增量, got %+v", pub.deltas)
	}
}

// 终稿超长截断。
func TestStream_SummaryTruncated(t *testing.T) {
	s, _, w := newTestStream("c1")
	long := make([]rune, maxSummaryRunes+100)
	for i := range long {
		long[i] = '字'
	}
	s.TapFor("planner").Done(string(long), "")
	got := []rune(w.appended[0].Content)
	if len(got) > maxSummaryRunes+10 {
		t.Fatalf("终稿应截断, got %d runes", len(got))
	}
	s.Close()
}

// 回归：flush 清空 pending 后，同 agent 下一窗口的增量只允许发布一帧。
// order 是首现序登记表，首现判定不得查 pending（flush 后恒为空，会让
// order 每周期重复追加同名道 → flush 按 order 逐项发布时同一帧放大 N 倍）。
func TestStream_FlushDoesNotAmplifyAcrossCycles(t *testing.T) {
	s, pub, _ := newTestStream("c1")
	tap := s.TapFor("monitor")

	tap.Delta("第1周期")
	s.flush()
	tap.Delta("第2周期")
	s.flush()
	tap.Delta("第3周期")
	s.flush()

	if len(pub.deltas) != 3 {
		t.Fatalf("3 个周期应只发布 3 帧, got %d: %+v", len(pub.deltas), pub.deltas)
	}
	s.mu.Lock()
	order := append([]string(nil), s.order...)
	s.mu.Unlock()
	if len(order) != 1 {
		t.Fatalf("order 应只登记一次首现序, got %v", order)
	}
	s.Close()
}

// 双通道：thinking 与 text 分道发布（thinking 先、text 后，与模型产出时序一致）；
// Done 终稿落库 metadata 携带 thinking 原文。
func TestStream_DualChannelThinkingAndText(t *testing.T) {
	s, pub, w := newTestStream("c1")
	tap := s.TapFor("executor")

	tap.ThinkingDelta("先想")
	tap.ThinkingDelta("一步")
	tap.Delta("后说")
	s.flush()

	if len(pub.deltas) != 2 {
		t.Fatalf("双通道应发布 2 帧, got %d: %+v", len(pub.deltas), pub.deltas)
	}
	if pub.deltas[0].channel != "thinking" || pub.deltas[0].text != "先想一步" {
		t.Fatalf("thinking 帧 = %+v", pub.deltas[0])
	}
	if pub.deltas[1].channel != "text" || pub.deltas[1].text != "后说" {
		t.Fatalf("text 帧 = %+v", pub.deltas[1])
	}

	tap.Done("结论", "完整思考")
	var meta struct {
		Kind      string `json:"kind"`
		AgentName string `json:"agent_name"`
		Thinking  string `json:"thinking"`
	}
	if err := json.Unmarshal(w.metas[len(w.metas)-1], &meta); err != nil {
		t.Fatalf("metadata 解析: %v", err)
	}
	if meta.Kind != "reasoning" || meta.AgentName != "executor" || meta.Thinking != "完整思考" {
		t.Fatalf("metadata = %+v", meta)
	}
	s.Close()
}
