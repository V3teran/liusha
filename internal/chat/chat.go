// Package chat 实现无 task 的纯聊天路径：消息分流闸（internal/intent）判定用户消息不是要下发
// 扫描（action）时，用便宜 LLM 作为通用渗透测试助手回答，不读黑板 finding、不触发扫描。
//
// 与 internal/qa 的分工：qa 就"已挖到的 finding"答（需 task 黑板）；chat 是无 task 语境的
// 闲聊/答疑（无 finding 依赖）。二者同样把回答落 assistant 消息并 publish SSE（前端实时显示）。
package chat

import (
	"context"
	"fmt"

	llm "github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/textx"
)

// Deps 是纯聊天所需的最小依赖（cmd/api 注入实现；是 qa.Deps 去掉 FindingsSummary 的子集）。
type Deps interface {
	// History 取本会话的对话上文（user/assistant，排除当前这条与 agent 过程
	// 事件，时间正序、已钳制）——多轮会话记忆的来源。读取失败返回 error。
	History(ctx context.Context, conversationID, excludeContent string) ([]llm.Message, error)
	// GenerateStream 流式生成：正文与扩展思考增量分别回调（可空；provider 不
	// 支持流式时退化为一次完整回调或不回调）。
	GenerateStream(ctx context.Context, msgs []llm.Message, tools []llm.ToolSchema, onText, onThinking func(chunk string)) (llm.Result, error)
	// PublishDelta 广播一帧增量（channel ∈ {text, thinking}；丢帧无害，终态消息兜底）。
	PublishDelta(ctx context.Context, conversationID, channel, text string) error
	// AppendAssistant 落 assistant 消息（KindMessage，metadata.thinking 携带
	// 思考终稿供前端折叠区回看），返回该消息的 SSE JSON payload。
	AppendAssistant(ctx context.Context, conversationID, content, thinking string) ([]byte, error)
	// Publish 把消息 payload 推 SSE。
	Publish(ctx context.Context, conversationID string, payload []byte) error
}

// Service 纯聊天服务。
type Service struct{ deps Deps }

// New 构造纯聊天服务。
func New(deps Deps) *Service { return &Service{deps: deps} }

const systemPrompt = `你是渗透测试助手，正在与用户闲聊或答疑（当前没有正在进行的扫描任务）。
用简洁中文回答用户的问题或消息。若用户想发起渗透测试/扫描，提示对方直接描述目标与测试类型即可开始。`

// maxUserMessageRunes 是当前用户消息进 LLM 上下文的上限（rune）。正常消息
// （含渗透测试常贴的请求/命令）远低于此；上限只防病态粘贴撑爆请求——本
// 路径没有修改链，入口限量是唯一防线。落库保留原文，仅进上下文时钳制。
const maxUserMessageRunes = 32000

// Answer 取会话上文 → 用通用助手回答当前消息 → 落 assistant 消息 + publish SSE。
// 不读 finding、不触发扫描。多轮对话：上文参与拼装（连续追问不失忆）。
func (s *Service) Answer(ctx context.Context, conversationID, message string) error {
	// 剔除当前消息按原文匹配（库里存的是原文），钳制只在构造末条消息时
	// 生效——顺序反了会让匹配失效，当前消息在历史与末尾各出现一次。
	history, err := s.deps.History(ctx, conversationID, message)
	if err != nil {
		return fmt.Errorf("读会话上文: %w", err)
	}
	msgs := make([]llm.Message, 0, len(history)+2)
	msgs = append(msgs, llm.Message{Role: llm.RoleSystem, Content: systemPrompt})
	msgs = append(msgs, history...)
	msgs = append(msgs, llm.Message{Role: llm.RoleUser, Content: textx.TruncateRunes(message, maxUserMessageRunes, "…（截断）")})

	// 流式生成：思考增量与正文增量分道广播（前端思考折叠区 + 打字机），
	// 全文以落库的 assistant 消息为准。
	res, err := s.deps.GenerateStream(ctx, msgs, nil,
		func(chunk string) { _ = s.deps.PublishDelta(ctx, conversationID, "text", chunk) },
		func(chunk string) { _ = s.deps.PublishDelta(ctx, conversationID, "thinking", chunk) },
	)
	if err != nil {
		return fmt.Errorf("生成回答: %w", err)
	}
	thinking := ""
	if res.Thinking != nil {
		thinking = res.Thinking.Text
	}
	payload, err := s.deps.AppendAssistant(ctx, conversationID, res.Content, thinking)
	if err != nil {
		return fmt.Errorf("落 assistant 消息: %w", err)
	}
	// publish best-effort：已落 PG，重连补历史可见，失败不返回错误
	// （推送失败在 scanAdapter.Publish 单点留痕）。
	_ = s.deps.Publish(ctx, conversationID, payload)
	return nil
}
