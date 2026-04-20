// Package llm 是 agent 对 LLM 的唯一适配层。
//
// 设计原则：Generator 接口**只在本包定义一次**。runtime/react 等上游直接引用
// llm.Generator，不重复定义同名接口以避免签名 drift。
package llm

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino-ext/components/model/deepseek"
	einoschema "github.com/cloudwego/eino/schema"
)

// Generator 是 ReAct 循环对 LLM 的最小依赖。
//
// 生产实现由本包的 NewDeepSeek 提供；测试使用 stub 即可。Plan 6 引入 per-role
// 模型映射时，运行时持有 `map[role]Generator`，接口签名不变。
type Generator interface {
	Generate(ctx context.Context, msgs []*einoschema.Message) (*einoschema.Message, error)
}

// deepseekAdapter 将 eino 的 ChatModel 适配到 Generator。
type deepseekAdapter struct {
	inner *deepseek.ChatModel
}

func (a *deepseekAdapter) Generate(ctx context.Context, msgs []*einoschema.Message) (*einoschema.Message, error) {
	return a.inner.Generate(ctx, msgs)
}

// NewDeepSeek 创建 DeepSeek Generator；fail-fast: 空 key / 空 base url 直接报错。
//
// Plan 5 骨架期所有 Role 共用同一个 Generator 实例。Plan 6 按 Role 映射模型时，
// 只需多次调用本函数构造多个 Generator，工厂函数签名无需变更。
func NewDeepSeek(apiKey, baseURL, modelID string, maxTokens int) (Generator, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("deepseek api key 未配置 (LIUSHA_DEEPSEEK_API_KEY)")
	}
	if baseURL == "" {
		return nil, fmt.Errorf("deepseek base url 未配置")
	}
	m, err := deepseek.NewChatModel(context.Background(), &deepseek.ChatModelConfig{
		APIKey:    apiKey,
		Model:     modelID,
		BaseURL:   baseURL,
		MaxTokens: maxTokens,
	})
	if err != nil {
		return nil, fmt.Errorf("初始化 DeepSeek model 失败: %w", err)
	}
	return &deepseekAdapter{inner: m}, nil
}
