// Package scanstream 用 Redis Pub/Sub 做 agent 过程事件的实时管道。
//
// 数据流（见记忆 project_phaseb_sse_arch）：runner 跑 agent 时把过程事件 publish 到
// channel `liusha:conv:{conversationID}`；api 的 SSE handler subscribe 同 channel 转发给前端。
//
// 职责单一——只做实时广播，不持久化：PG 的 conversation message（internal/conversation）才是
// 真相源，断线重连走 ListMessages(afterSeq) 补历史。故 pub/sub（订阅者不在线即丢）足够，
// 不用 Redis Stream（避免 PG/Redis 双真相源）。
//
// payload 是已序列化的字节（runner 传 conversation.Message 的 JSON）；本包不解释内容、
// 不依赖 conversation/scanagent，纯传输层。
package scanstream

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// channelPrefix 是会话事件 channel 的前缀；完整 channel = prefix + conversationID。
const channelPrefix = "liusha:conv:"

// Channel 返回某会话的事件 channel 名（Publisher / Subscriber 共用，保证拼法一致）。
func Channel(conversationID string) string { return channelPrefix + conversationID }

// Publisher 把过程事件 publish 到会话 channel（runner 侧）。
type Publisher struct {
	rdb *redis.Client
}

// NewPublisher 用 redis client 构造 Publisher。
func NewPublisher(rdb *redis.Client) *Publisher { return &Publisher{rdb: rdb} }

// Publish 把 payload 广播到会话 channel。conversationID 为空时静默跳过（无会话不发）。
// best-effort：订阅者不在线时 redis 返回 0 接收者，不算错误（实时流允许丢，PG 是真相源）。
func (p *Publisher) Publish(ctx context.Context, conversationID string, payload []byte) error {
	if conversationID == "" {
		return nil
	}
	if err := p.rdb.Publish(ctx, Channel(conversationID), payload).Err(); err != nil {
		return fmt.Errorf("scanstream publish conv=%s: %w", conversationID, err)
	}
	return nil
}

// DeltaFrame 是流式增量帧：agent 输出的逐段实时广播，不持久化——
// 终态以落库的 conversation message 为准，前端收到终态消息即清空对应气泡。
// delta:true 是 SSE handler 的帧类型判别位；channel 区分双通道：
//
//	"thinking" — 扩展思考增量（模型内部 CoT，先流）
//	"text"     — 正文增量（ReAct thought / 最终回答，后流）
//
// channel 缺省按 "text" 解读（旧帧兼容）。
type DeltaFrame struct {
	Delta     bool   `json:"delta"`
	AgentName string `json:"agent_name,omitempty"` // 产生增量的 agent（chat 助手为 assistant）
	Channel   string `json:"channel,omitempty"`    // "thinking" | "text"（缺省 text）
	Text      string `json:"text"`
}

// 增量通道常量（SSE 帧与前端共用的字面量契约）。
const (
	ChannelText     = "text"
	ChannelThinking = "thinking"
)

// PublishDelta 广播一帧增量（AgentName 为空按 assistant 归档；channel 见
// DeltaFrame.Channel，空按 text）。与 Publish 同样 best-effort：丢帧无害，终态消息兜底。
func (p *Publisher) PublishDelta(ctx context.Context, conversationID, agentName, channel, text string) error {
	if conversationID == "" || text == "" {
		return nil
	}
	if agentName == "" {
		agentName = "assistant"
	}
	if channel == "" {
		channel = ChannelText
	}
	payload, err := json.Marshal(DeltaFrame{Delta: true, AgentName: agentName, Channel: channel, Text: text})
	if err != nil {
		return fmt.Errorf("scanstream marshal delta: %w", err)
	}
	return p.Publish(ctx, conversationID, payload)
}
