// Package llm 是框架层 LLM 抽象——Provider 契约 + 类型事实源 + 协议适配器。
//
// 分层（Ports & Adapters）：
//   - 本包定义 Provider 契约（Complete/CountTokens）与全部线类型
//     （Message/ToolCall/Usage...），并持有唯一的协议适配器实现
//     （openai_compat / anthropic，血统为生产验证过的 4 月版本）。
//   - 业务侧（cmd/api 等）按 role 归档（llmcfg.AgentComplexity）后走本包 Router；
//     复杂度归档表在 internal/config/llm（DB 配置优先，代码兜底）。
//
// 依赖方向：本包不 import 任何业务包。
package llm

import (
	"context"
)

// Provider 是 LLM 调用的统一抽象。实现者负责协议差异屏蔽。
//
// 并发安全：同一 Provider 实例可被多个 goroutine 并发调用。
type Provider interface {
	// Complete 发起单次非流式调用，等待完整响应。
	Complete(ctx context.Context, req Request) (Response, error)

	// CountTokens 计算 req 的输入 token 数（OpenAI 兼容层为本地估算）。
	// 不消耗生成配额。
	CountTokens(ctx context.Context, req Request) (int, error)

	// ModelID 返回底层模型标识。
	ModelID() string

	// ProviderID 返回 provider 标识，如 "anthropic" / "openai"。
	ProviderID() string
}

// Request 是一次 LLM 调用的完整输入。
type Request struct {
	Messages  []Message    // 会话历史，含 system/user/assistant/tool
	Tools     []ToolSchema // 可调用工具声明；空表示纯文本对话
	MaxTokens int          // 0 = provider 默认值
	// Temperature 采样温度（0-1）；nil = provider 默认。指针区分「未设置」与 0
	// （0 = 贪心解码是有效语义，不能当默认吞掉）。
	Temperature *float64
}

// Response 是非流式调用的完整输出。
type Response struct {
	Content      string     // 文本内容（ToolCalls 非空时可能为空）
	ToolCalls    []ToolCall // 工具调用请求列表
	Usage        Usage
	FinishReason string // "stop" | "tool_use" | "max_tokens" | "error"
}
