// Package qa 实现多轮会话的问答路径：读 owner 黑板 finding，用便宜 LLM 就已挖结果回答，
// 不触发扫描。回答落 assistant 消息并 publish SSE（前端实时显示）。
package qa

import (
	"context"
	"fmt"

	llm "github.com/V3teran/liusha/internal/framework/llm"
)

// Deps 是问答所需的最小依赖（cmd/api 注入实现）。
type Deps interface {
	// FindingsSummary 返回该 (conversationID, taskID) 关联 owner 的 finding 文本摘要。
	FindingsSummary(ctx context.Context, conversationID, taskID string) (string, error)
	// GenerateStream 流式生成：正文与扩展思考增量分别回调（可空）。
	GenerateStream(ctx context.Context, msgs []llm.Message, tools []llm.ToolSchema, onText, onThinking func(chunk string)) (llm.Result, error)
	// PublishDelta 广播一帧增量（channel ∈ {text, thinking}）。
	PublishDelta(ctx context.Context, conversationID, channel, text string) error
	// AppendAssistant 落 assistant 消息（KindMessage，metadata.thinking 携带
	// 思考终稿供前端折叠区回看），返回该消息的 SSE JSON payload。
	AppendAssistant(ctx context.Context, conversationID, content, thinking string) ([]byte, error)
	// Publish 把消息 payload 推 SSE。
	Publish(ctx context.Context, conversationID string, payload []byte) error
}

// Service 问答服务。
type Service struct{ deps Deps }

// New 构造问答服务。
func New(deps Deps) *Service {
	return &Service{deps: deps}
}

const systemPrompt = `你是渗透测试助手。**只依据下面已挖到的 finding 回答用户问题**，不要编造未列出的漏洞。
若 finding 为空或不足以回答，如实说明"目前还没挖到相关结果"。回答简洁中文。`

// Answer 读黑板 → LLM 生成回答 → 落 assistant 消息 + publish SSE。
func (s *Service) Answer(ctx context.Context, conversationID, taskID, question string) error {
	findings, err := s.deps.FindingsSummary(ctx, conversationID, taskID)
	if err != nil {
		return fmt.Errorf("读 finding: %w", err)
	}
	// 流式生成：思考增量与正文增量分道广播（前端思考折叠区 + 打字机），
	// 全文以落库的 assistant 消息为准。
	res, err := s.deps.GenerateStream(ctx, []llm.Message{
		{Role: llm.RoleSystem, Content: systemPrompt},
		{Role: llm.RoleUser, Content: "已挖到的 finding：\n" + findings + "\n\n用户问题：" + question},
	}, nil,
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
	// publish best-effort：已落 PG，重连补历史可见，失败不返回错误。
	_ = s.deps.Publish(ctx, conversationID, payload)
	return nil
}
