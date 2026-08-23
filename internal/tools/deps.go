// Package tools 提供所有17个域工具的实现，每个工具实现 registry.Tool 接口。
//
// 工具按功能域分文件：control / credentials / findings / corpus / lead / traffic / sandbox / skill。
// register.go 提供 RegisterAll 一次性注入所有工具到 registry.Registry。
package tools

import (
	"github.com/V3teran/liusha/internal/corpus"
	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/lead"
	"github.com/V3teran/liusha/internal/sandbox"
	"github.com/V3teran/liusha/internal/skill"
	"github.com/V3teran/liusha/internal/traffic"
)

// Deps 持有单次 agent run 所需的全部上下文与依赖。
// 每次 handleSolo/handleSwarm 调用时从 handler 字段 + 运行时参数组装。
type Deps struct {
	TaskID     string
	ExecutorID string
	Host       string

	Findings   *finding.Store
	Corpus     *corpus.Store
	Embedder   corpus.Embedder  // 可 nil → 退化为纯 sparse 检索
	Reranker   corpus.Reranker
	Leads      *lead.Store
	ProxyStore *traffic.ProxyStore
	AgentStore *traffic.AgentStore
	Creds      credential.Provider
	Sandbox    sandbox.Client   // Spawn 后注入，可 nil
	ToolingLoader *skill.Loader
	VulnLoader    *skill.Loader
}
