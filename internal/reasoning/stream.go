// Package reasoning 聚合 agent 输出的流式增量并接入会话时间线：
//
//   - 增量按 agent × 通道双桶合批（150ms 窗口）经 scanstream 实时广播——不持久化，
//     丢帧无害。通道分 two lanes：thinking（模型内部 CoT，先流）与 text
//     （ReAct thought 正文，后流），前端分两个展示区（思考折叠区 + 正文打字机）；
//   - Done 终稿落 conversation message（kind=event + metadata{kind:reasoning,
//     agent_name, thinking}）并广播——PG 是断线重连的真相源，前端收到终态即清空
//     该 agent 的活动气泡。
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
	"github.com/V3teran/liusha/internal/framework/runtime"
	"github.com/V3teran/liusha/internal/scanstream"
)

// flushInterval 是增量合批窗口：窗口内同 agent 同通道的 chunk 合并成一帧发布，
// 封顶 Redis/SSE 帧率（4 agent 并发 × 逐 token 帧会打爆实时通道）。
const flushInterval = 150 * time.Millisecond

// maxSummaryRunes 是终稿落库的长度上限（正文与思考各自截断）：planner 终稿可能
// 是完整 JSON 动作清单、思考可能长达数千 token，时间线只需要可读的过程与结论。
const maxSummaryRunes = 4000

// Round 是一个 agent 一轮的终态产出：正文与思考终稿 + 该轮用量元信息
// （token 累计与墙钟耗时，时间线每步 chip 的数据源）。Raw 携带协议原文——
// 当正文是解析后的人读叙述（如规划/裁决/执行协议）时，原始输出随折叠区可查。
type Round struct {
	Summary   string
	Thinking  string
	Raw       string
	InTokens  int
	OutTokens int
	LatencyMs int64
}

// Tap 是单个 agent 的输出流出口。Delta 记正文增量（ReAct thought）；
// ThinkingDelta 记扩展思考增量（模型内部 CoT）；Done 结束当前一轮——
// Round 携带终稿与用量（落库 + 广播，前端据此清空气泡并渲染用量 chip）。
// 三者并发安全。
type Tap interface {
	Delta(chunk string)
	ThinkingDelta(chunk string)
	Done(round Round)
}

// RoundOf 把 ReAct 运行终稿折成时间线一轮的载荷（四 agent 共用，字段映射单一出处）。
func RoundOf(res *runtime.ReActResult) Round {
	return Round{
		Summary:   res.FinalAnswer,
		Thinking:  res.FinalThinking,
		InTokens:  res.Usage.InTokens,
		OutTokens: res.Usage.OutTokens,
		LatencyMs: res.ElapsedMs,
	}
}

// Publisher 是增量与终态的实时广播通道（*scanstream.Publisher 满足）。
type Publisher interface {
	PublishDelta(ctx context.Context, conversationID, agentName, channel, text string) error
	Publish(ctx context.Context, conversationID string, payload []byte) error
}

// Writer 是终态消息的落库通道（*conversation.Store 满足）。
type Writer interface {
	AppendMessage(ctx context.Context, convID string, role conversation.Role, kind conversation.Kind, content string, metadata json.RawMessage) (conversation.Message, error)
}

// laneBuffer 是单个 agent 的待发增量双通道桶。
type laneBuffer struct {
	text     string
	thinking string
}

// Stream 是会话级的输出流聚合器：按 agent 分桶（桶内双通道），一个后台
// flusher 统一发布。
type Stream struct {
	pub    Publisher
	writer Writer
	convID string
	logger zerolog.Logger

	mu      sync.Mutex
	pending map[string]*laneBuffer // agent → 待发增量
	order   []string               // agent 首次出现序（发布与前端渲染保序）

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
}

// NewStream 构造会话输出流。convID 为空时返回 no-op 流（后台扫描无会话不发）。
func NewStream(pub Publisher, writer Writer, convID string, logger zerolog.Logger) *Stream {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Stream{
		pub: pub, writer: writer, convID: convID, logger: logger,
		pending: map[string]*laneBuffer{},
		ctx:     ctx, cancel: cancel, done: make(chan struct{}),
	}
	if convID == "" {
		close(s.done) // no-op：无 flusher 循环
		return s
	}
	go s.flushLoop()
	return s
}

// TapFor 返回指定 agent 的输出流出口。
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

// lane 返回（必要时创建）agent 的增量桶；首现序登记查 order 本身——
// flush 清空 pending 不清 order，若查 pending 会让每个合批周期重复追加
// 同名道，发布时同一帧被放大 N 倍（历史 bug，回归测试锁死）。
// 调用方持有 s.mu。
func (s *Stream) lane(agent string) *laneBuffer {
	buf, ok := s.pending[agent]
	if !ok {
		buf = &laneBuffer{}
		s.pending[agent] = buf
		if !s.registered(agent) {
			s.order = append(s.order, agent)
		}
	}
	return buf
}

// registered 报告 agent 是否已登记过首现序（调用方持有 s.mu）。
func (s *Stream) registered(agent string) bool {
	for _, a := range s.order {
		if a == agent {
			return true
		}
	}
	return false
}

// flush 把当前待发增量按 agent 快照后发布（锁内快照、锁外网络调用）。
// 同 agent 内先发 thinking 后发 text——与模型产出时序一致。
func (s *Stream) flush() {
	s.mu.Lock()
	if len(s.pending) == 0 {
		s.mu.Unlock()
		return
	}
	snapshot, order := s.pending, s.order
	s.pending = map[string]*laneBuffer{}
	s.mu.Unlock()

	for _, agent := range order {
		buf := snapshot[agent]
		if buf == nil {
			continue
		}
		s.publishLane(agent, buf)
	}
}

// publishLane 发布一个 agent 桶的双通道增量（thinking 先、text 后）。
func (s *Stream) publishLane(agent string, buf *laneBuffer) {
	if buf.thinking != "" {
		if err := s.pub.PublishDelta(s.ctx, s.convID, agent, scanstream.ChannelThinking, buf.thinking); err != nil {
			s.logger.Warn().Err(err).Str("agent", agent).Msg("思考增量广播失败（丢帧无害）")
		}
	}
	if buf.text != "" {
		if err := s.pub.PublishDelta(s.ctx, s.convID, agent, scanstream.ChannelText, buf.text); err != nil {
			s.logger.Warn().Err(err).Str("agent", agent).Msg("正文增量广播失败（丢帧无害）")
		}
	}
}

// agentTap 绑定 agent 名的流出口。
type agentTap struct {
	s     *Stream
	agent string
}

// Delta 记一段正文增量，等待合批窗口发布。
func (t agentTap) Delta(chunk string) {
	if chunk == "" || t.s.convID == "" {
		return
	}
	t.s.mu.Lock()
	t.s.lane(t.agent).text += chunk
	t.s.mu.Unlock()
}

// ThinkingDelta 记一段扩展思考增量，等待合批窗口发布。
func (t agentTap) ThinkingDelta(chunk string) {
	if chunk == "" || t.s.convID == "" {
		return
	}
	t.s.mu.Lock()
	t.s.lane(t.agent).thinking += chunk
	t.s.mu.Unlock()
}

// Done 结束当前一轮：先冲刷该 agent 的待发增量（保序），再落终态消息并广播。
// 终稿为空时仍落一条终态——终态帧是前端清空气泡的信号，不可省。
func (t agentTap) Done(round Round) {
	if t.s.convID == "" {
		return
	}
	t.s.mu.Lock()
	buf := t.s.pending[t.agent]
	delete(t.s.pending, t.agent)
	t.s.mu.Unlock()

	if buf != nil {
		t.s.publishLane(t.agent, buf)
	}
	t.publishFinal(round)
}

// publishFinal 落库并广播终态 reasoning 消息（思考原文与用量随 metadata 携带，
// 前端终态卡以折叠区呈现思考、以 chip 呈现 token/耗时，正文以消息 Content 呈现）。
func (t agentTap) publishFinal(round Round) {
	meta := map[string]any{
		"kind":       "reasoning",
		"agent_name": t.agent,
		"thinking":   truncateRunes(round.Thinking, maxSummaryRunes),
		"in_tokens":  round.InTokens,
		"out_tokens": round.OutTokens,
		"latency_ms": round.LatencyMs,
	}
	if round.Raw != "" {
		meta["raw"] = truncateRunes(round.Raw, maxSummaryRunes)
	}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		t.s.logger.Warn().Err(err).Str("agent", t.agent).Msg("reasoning 元数据序列化失败")
		return
	}
	msg, err := t.s.writer.AppendMessage(t.s.ctx, t.s.convID,
		conversation.RoleAssistant, conversation.KindEvent, truncateRunes(round.Summary, maxSummaryRunes), metaJSON)
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
