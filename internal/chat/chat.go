// Package chat 实现无 task 的纯聊天路径：意图闸（internal/intent）判定用户消息不是要下发
// 扫描（action）时，用便宜 LLM 作为通用渗透测试助手回答，不读黑板 finding、不触发扫描。
//
// 与 internal/qa 的分工：qa 就"已挖到的 finding"答（需 task 黑板）；chat 是无 task 语境的
// 闲聊/答疑（无 finding 依赖）。二者同样把回答落 assistant 消息并 publish SSE（前端实时显示）。
package chat

import (
	"context"
	"fmt"

	"github.com/V3teran/liusha/internal/llm"
)

// Deps 是纯聊天所需的最小依赖（cmd/api 注入实现；是 qa.Deps 去掉 FindingsSummary 的子集）。
type Deps interface {
	// Generate 调便宜 LLM。
	Generate(ctx context.Context, msgs []llm.Message, tools []llm.ToolSchema) (llm.Result, error)
	// AppendAssistant 落 assistant 消息（KindMessage），返回该消息的 SSE JSON payload。
	AppendAssistant(ctx context.Context, conversationID, content string) ([]byte, error)
	// Publish 把消息 payload 推 SSE。
	Publish(ctx context.Context, conversationID string, payload []byte) error
}

// Service 纯聊天服务。
type Service struct{ deps Deps }

// New 构造纯聊天服务。
func New(deps Deps) *Service { return &Service{deps: deps} }

const systemPrompt = `你是渗透测试助手，正在与用户闲聊或答疑（当前没有正在进行的扫描任务）。
用简洁中文回答用户的问题或消息。若用户想发起渗透测试/扫描，提示对方直接描述目标与测试类型即可开始。`

// Answer 用通用助手回答用户消息 → 落 assistant 消息 + publish SSE。不读 finding、不触发扫描。
func (s *Service) Answer(ctx context.Context, conversationID, message string) error {
	res, err := s.deps.Generate(ctx, []llm.Message{
		{Role: llm.RoleSystem, Content: systemPrompt},
		{Role: llm.RoleUser, Content: message},
	}, nil)
	if err != nil {
		return fmt.Errorf("生成回答: %w", err)
	}
	payload, err := s.deps.AppendAssistant(ctx, conversationID, res.Content)
	if err != nil {
		return fmt.Errorf("落 assistant 消息: %w", err)
	}
	// publish best-effort：已落 PG，重连补历史可见，失败不返回错误。
	_ = s.deps.Publish(ctx, conversationID, payload)
	return nil
}
