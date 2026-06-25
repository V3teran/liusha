package main

import (
	"context"

	"github.com/V3teran/liusha/internal/llm"
)

// llmSummarizer 把 llm.Router 适配成 attackgraph.Summarizer（执行图里程碑摘要用）。
// role="inspector"：复用 config 里 inspector→light_provider 的便宜模型路由（摘要属轻量分析）。
type llmSummarizer struct {
	router *llm.Router
}

// Summarize 单轮 user 消息调 light LLM 拿一句总结。无工具、无系统提示，纯文本生成。
func (s llmSummarizer) Summarize(ctx context.Context, prompt string) (string, error) {
	g, err := s.router.For(ctx, "inspector")
	if err != nil {
		return "", err
	}
	res, err := g.Generate(ctx, []llm.Message{{Role: llm.RoleUser, Content: prompt}}, nil)
	if err != nil {
		return "", err
	}
	return res.Content, nil
}
