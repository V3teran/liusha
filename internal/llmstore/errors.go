package llmstore

import "fmt"

// UnresolvedError 表示 role 未能解析到 provider key——role 经 AgentTier 归档后，该档及隐式默认
// 档（heavy）均无绑定的 provider，或全局备胎 __fallback__ 未配置。单独成类型：两个 LLM 工厂据此
// 报「路由未配置」而非「provider 不存在」，语义更准，调用方可类型断言区分。
type UnresolvedError struct {
	Role     string // 按 role 解析时置位
	Fallback bool   // 解析全局备胎（__fallback__）时置位
}

func (e *UnresolvedError) Error() string {
	if e.Fallback {
		return "llm 路由未配置：全局备胎（__fallback__）未绑定 provider"
	}
	return fmt.Sprintf("llm 路由未配置：role %q 所属能力档及隐式默认档（heavy）均未绑定 provider", e.Role)
}
