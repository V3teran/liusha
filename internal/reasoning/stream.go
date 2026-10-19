// Package reasoning 聚合 agent 思考的流式增量并接入会话时间线：
//
//   - Delta 增量按 agent 合批（150ms 窗口）经 scanstream 实时广播——不持久化，
//     丢帧无害；
//   - Done 终稿落 conversation message（kind=event + metadata{kind:reasoning,
//     agent_name}）并广播——PG 是断线重连的真相源，前端收到终态即清空该 agent
//     的打字机气泡。
//
// 认知循环四个 agent 并发思考，Stream 按 agent 分桶合批、保序广播；
// convID 为空（纯后台扫描）时整体 no-op。
package reasoning

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/conversation"
)

// flushInterval 是增量合批窗口：窗口内同 agent 的 chunk 合并成一帧发布，
// 封顶 Redis/SSE 帧率（4 agent 并发 × 逐 token 帧会打爆实时通道）。
const flushInterval = 150 * time.Millisecond

// maxSummaryRunes 是终稿落库的长度上限：planner 终稿可能是完整 JSON 动作清单，
// 时间线只需要可读的裁决/结论，超长截断。
const maxSummaryRunes = 4000

// Tap 是单个 agent 的思考流出口。Delta 逐段喂思考增量；Done 结束当前一轮，
// summary 为该轮终稿（落库 + 广播，前端据此清空气泡）。两者并发安全。
type Tap interface {
	Delta(chunk string)
	Done(summary string)
}

// Publisher 是增量与终态的实时广播通道（*scanstream.Publisher 满足）。
type Publisher interface {
	PublishDelta(ctx context.Context, conversationID, agentName, text string) error
	Publish(ctx context.Context, conversationID string, payload []byte) error
}

// Writer 是终态消息的落库通道（*conversation.Store 满足）。
type Writer interface {
	AppendMessage(ctx context.Context, convID string, role conversation.Role, kind conversation.Kind, content string, metadata json.RawMessage) (conversation.Message, error)
}

// Stream 是会话级的思考流聚合器：按 agent 分桶合批，一个后台 flusher 统一发布。
type Stream struct {
	pub    Publisher
	writer Writer
	convID string
	logger zerolog.Logger

	mu      sync.Mutex
	pending map[string]string // agent → 待发增量
	order   []string          // agent 首次出现序（发布与前端渲染保序）

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
}

// NewStream 构造会话思考流。convID 为空时返回 no-op 流（后台扫描无会话不发）。
func NewStream(pub Publisher, writer Writer, convID string, logger zerolog.Logger) *Stream {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Stream{
		pub: pub, writer: writer, convID: convID, logger: logger,
		pending: map[string]string{},
		ctx:     ctx, cancel: cancel, done: make(chan struct{}),
	}
	if convID == "" {
		close(s.done) // no-op：无 flusher 循环
		return s
	}
	go s.flushLoop()
	return s
}

// TapFor 返回指定 agent 的思考流出口。
func (s *Stream) TapFor(agent string) Tap { return agentTap{s: s, agent: agent} }

// Close 冲刷全部待发增量并停止 flusher（认知循环结束时调用）。
func (s *Stream) Close() {
	s.cancel()
	<-s.done
	s.flush()
}

func (s *Stream) flushLoop() {
	defer close(s.done)
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.flush()
		}
	}
}

// flush 把当前待发增量按 agent 快照后发布（锁内快照、锁外网络调用）。
func (s *Stream) flush() {
	s.mu.Lock()
	if len(s.pending) == 0 {
		s.mu.Unlock()
		return
	}
	snapshot, order := s.pending, s.order
	s.pending = map[string]string{}
	s.mu.Unlock()

	for _, agent := range order {
		text := snapshot[agent]
		if text == "" {
			continue
		}
		if err := s.pub.PublishDelta(s.ctx, s.convID, agent, text); err != nil {
			s.logger.Warn().Err(err).Str("agent", agent).Msg("思考增量广播失败（丢帧无害）")
		}
	}
}

// agentTap 绑定 agent 名的流出口。
type agentTap struct {
	s     *Stream
	agent string
}

// Delta 记一段思考增量，等待合批窗口发布。
func (t agentTap) Delta(chunk string) {
	if chunk == "" || t.s.convID == "" {
		return
	}
	t.s.mu.Lock()
	if _, ok := t.s.pending[t.agent]; !ok {
		t.s.order = append(t.s.order, t.agent)
	}
	t.s.pending[t.agent] += chunk
	t.s.mu.Unlock()
}

// Done 结束当前一轮：先冲刷该 agent 的待发增量（保序），再落终态消息并广播。
// summary 为空时仍落一条空摘要消息——终态帧是前端清空气泡的信号，不可省。
func (t agentTap) Done(summary string) {
	if t.s.convID == "" {
		return
	}
	t.s.mu.Lock()
	text := t.s.pending[t.agent]
	delete(t.s.pending, t.agent)
	t.s.mu.Unlock()

	if text != "" {
		if err := t.s.pub.PublishDelta(t.s.ctx, t.s.convID, t.agent, text); err != nil {
			t.s.logger.Warn().Err(err).Str("agent", t.agent).Msg("思考增量广播失败（丢帧无害）")
		}
	}
	t.publishFinal(summary)
}

// publishFinal 落库并广播终态 reasoning 消息。
func (t agentTap) publishFinal(summary string) {
	summary = truncateRunes(summary, maxSummaryRunes)
	meta, err := json.Marshal(map[string]string{
		"kind":       "reasoning",
		"agent_name": t.agent,
	})
	if err != nil {
		t.s.logger.Warn().Err(err).Str("agent", t.agent).Msg("reasoning 元数据序列化失败")
		return
	}
	msg, err := t.s.writer.AppendMessage(t.s.ctx, t.s.convID,
		conversation.RoleAssistant, conversation.KindEvent, summary, meta)
	if err != nil {
		t.s.logger.Warn().Err(err).Str("agent", t.agent).Msg("reasoning 终稿落库失败（气泡清理将延迟到重连）")
		return
	}
	payload, err := json.Marshal(msg)
	if err != nil {
		return
	}
	if err := t.s.pub.Publish(t.s.ctx, t.s.convID, payload); err != nil {
		t.s.logger.Warn().Err(err).Str("agent", t.agent).Msg("reasoning 终态广播失败（重连补历史可见）")
	}
}

// truncateRunes 按 rune 截断并追加省略标记。
func truncateRunes(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + "…（截断）"
}

// 确认接口满足关系（编译期断言）。
var _ Tap = agentTap{}
