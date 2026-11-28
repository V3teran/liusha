// Package tools 提供跨角色共享的 function calling 域工具，每个工具实现 registry.Tool 接口；
// 角色专属工具（planner 的 graph_read、monitor 的 decision_publish、evaluator 的
// repro_replay 等）在各角色包内自建。
//
// 工具按功能域分文件：credentials / corpus / websearch / traffic / sandbox / skill。
// register.go 提供 RegisterAll 一次性注入所有工具到 registry.ToolRegistry。
package tools

import (
	"context"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/corpus"
	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/sandbox"
	"github.com/V3teran/liusha/internal/skill"
	"github.com/V3teran/liusha/internal/task"
	"github.com/V3teran/liusha/internal/traffic"
)

// Deps 持有单次 agent run 所需的全部上下文与依赖。
// 每次任务执行（runner.executeTask）时从 handler 字段 + 运行时参数组装。
type Deps struct {
	TaskID       string
	AssignmentID string // 批次 ID（沙盒工作区一级分组 /root/liusha/workspace/<assignmentID>/<taskID>）
	AgentRunID   string // 本轮认知循环的 agent_run.id（工具调用/证据归属）
	Host         string
	// Logger 是任务作用域 logger（cognition 注入，自动带 task_id）；工具内部的
	// 降级/异常路径（如 store 查询失败）经它留痕。零值 = 丢弃输出（测试用）。
	Logger zerolog.Logger

	Tasks       *task.Store
	Corpus      *corpus.Service
	CorpusScope corpus.Scope // corpus_search 的可见范围
	// WebSearch 是公开网页的检索后端（部署方接入的搜索接口）；nil 时 web_search 不注册。
	WebSearch  WebSearcher
	ProxyStore *traffic.ProxyStore
	AgentStore *traffic.AgentStore
	Creds      credential.Provider
	Sandbox    CommandRunner // 沙盒命令执行（command_run / browser_drive）；nil 时这两个工具不注册
	Skills     skill.Reader  // skill 渐进式加载（按 agent.skills 建的白名单视图；nil 不注册 skill_read）

	// StepToolTimeoutSeconds 是 command_run 的超时钳制上限秒（runtime.step_tool_timeout_seconds：
	// 配置里的出厂值或页面上的热改值，任务开始时快照）。注册表级超时与 LLM 自传 timeout_seconds
	// 的钳制上界同源，杜绝「配置 1800 实际 120」的三重失效。构造 command_run 时要求为正——
	// 工具不内置出厂值的第二份。
	StepToolTimeoutSeconds int
	// 沙盒对外可达性契约（cognition 装配层注入）：command_run 工具描述据此
	// 告知 LLM 可绑的对外监听端口段与 payload 回连地址。
	ListenerPortStart int
	ListenerPortCount int
	CallbackHost      string

	// OutputWindowBytes 是单条工具结果进模型上下文的窗口（runtime.tool_output_window_bytes：配置里的
	// 出厂值或页面上的热改值）。command_run 不裁剪输出——裁剪只在工具结果进上下文的那一道窗口做；
	// 这个值交给沙盒当落盘阈值：命令输出超过窗口时由沙盒把完整内容落盘，窗口省掉的部分因此有处可读。
	// 构造 command_run 时要求为正。
	OutputWindowBytes int
}

// CommandRunner 是工具层对沙盒的唯一依赖：执行一条命令（*sandbox.HTTPClient 满足）。
type CommandRunner interface {
	Exec(ctx context.Context, req sandbox.ExecRequest) (sandbox.ExecResult, error)
}
