// Package tools 提供跨角色共享的 function calling 域工具（共 11 个），每个工具实现
// registry.Tool 接口；角色专属工具（planner 的 observe_state、monitor 的
// publish_decision、evaluator 的 replay_for_verification 等）在各角色包内自建。
//
// 工具按功能域分文件：credentials / corpus / insight / traffic / sandbox / skill。
// register.go 提供 RegisterAll 一次性注入所有工具到 registry.ToolRegistry。
package tools

import (
	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/corpus"
	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/insight"
	"github.com/V3teran/liusha/internal/sandbox"
	"github.com/V3teran/liusha/internal/skill"
	"github.com/V3teran/liusha/internal/task"
	"github.com/V3teran/liusha/internal/traffic"
)

// Deps 持有单次 agent run 所需的全部上下文与依赖。
// 每次任务执行（runner.executeTask）时从 handler 字段 + 运行时参数组装。
type Deps struct {
	TaskID     string
	AgentRunID string // 本轮认知循环的 agent_run.id（工具调用/证据归属）
	Host       string
	// Logger 是任务作用域 logger（cognition 注入，自动带 task_id）；工具内部的
	// 降级/异常路径（如 store 查询失败）经它留痕。零值 = 丢弃输出（测试用）。
	Logger zerolog.Logger

	Tasks      *task.Store
	Corpus     *corpus.Store
	Embedder   corpus.Embedder // 可 nil → 退化为纯 sparse 检索
	Reranker   corpus.Reranker
	Insights   *insight.Store
	ProxyStore *traffic.ProxyStore
	AgentStore *traffic.AgentStore
	Creds      credential.Provider
	Sandbox    sandbox.Client // Spawn 后注入，可 nil
	Skills     skill.Reader   // skill 渐进式加载（按 agent.skills 建的白名单视图；nil 不注册 read_skill）

	// StepToolTimeoutSeconds 是 run_command 的超时钳制上限秒（系统配置 runtime 组，
	// 任务开始时快照；0=内置默认 120s）。注册表级超时与 LLM 自传 timeout_seconds
	// 的钳制上界同源，杜绝「配置 1800 实际 120」的三重失效。
	StepToolTimeoutSeconds int
	// RunTailBytes 是 run_command 输出保留尾部的字节数（0=内置默认 8192）。
	RunTailBytes int
}
