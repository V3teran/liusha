// Package tools 提供跨角色共享的 function calling 域工具（共 11 个），每个工具实现
// registry.Tool 接口；角色专属工具（planner 的 observe_state、monitor 的
// publish_decision、evaluator 的 replay_for_verification 等）在各角色包内自建。
//
// 工具按功能域分文件：credentials / corpus / insight / traffic / sandbox / skill。
// register.go 提供 RegisterAll 一次性注入所有工具到 registry.Registry。
package tools

import (
	"github.com/V3teran/liusha/internal/corpus"
	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/insight"
	"github.com/V3teran/liusha/internal/sandbox"
	"github.com/V3teran/liusha/internal/skill"
	"github.com/V3teran/liusha/internal/task"
	"github.com/V3teran/liusha/internal/traffic"
)

// Deps 持有单次 agent run 所需的全部上下文与依赖。
// 每次 handleCognition 调用时从 handler 字段 + 运行时参数组装。
type Deps struct {
	TaskID     string
	AgentRunID string // 本轮认知循环的 agent_run.id（工具调用/证据归属）
	Host       string

	Tasks      *task.Store
	Findings   *finding.Store
	Corpus     *corpus.Store
	Embedder   corpus.Embedder // 可 nil → 退化为纯 sparse 检索
	Reranker   corpus.Reranker
	Insights   *insight.Store
	ProxyStore *traffic.ProxyStore
	AgentStore *traffic.AgentStore
	Creds      credential.Provider
	Sandbox    sandbox.Client // Spawn 后注入，可 nil
	Skills     skill.Reader   // skill 渐进式加载（按 agent.skills 建的白名单视图；nil 不注册 read_skill）
}
