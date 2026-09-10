// Package verifier - 简化的工具集
package evaluator

// registerTools 注册 Evaluator 专用工具（简化版）
func (a *Agent) registerTools() {
	// 当前简化实现：不注册任何工具
	// 完整版需要实现 replay_traffic, check_response, conclude

	// TODO: 实现完整的评估工具
	_ = a.registry
	_ = a.traffic
}
