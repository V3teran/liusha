package llmstore

import "fmt"

// UnresolvedError 表示 role 未能解析到 provider key——role 经 AgentComplexity 归档后，该档及
// 隐式默认档（medium）均无绑定的 provider。单独成类型：两个 LLM 工厂据此报「路由未配置」而非
// 「provider 不存在」，语义更准，调用方可类型断言区分。
type UnresolvedError struct {
	Role string // 按 role 解析时置位
}

func (e *UnresolvedError) Error() string {
	return fmt.Sprintf("llm 路由未配置：role %q 所属能力档及隐式默认档（medium）均未绑定 provider", e.Role)
}
