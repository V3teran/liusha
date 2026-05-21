// Package skill 此文件定义striker ReAct skill 装配的接口约定（Builder + BuilderParams），
// 与 Loader 同包：skill 包代表"skill 系统"——加载 SKILL.md（Card / Loader）
// 与装配可执行 react.Config（Builder）两个职责合并在一处。
//
// 解耦关系：
//   - tools/scan 只负责"按 skill 名调 Builder + 跑striker ReAct"
//   - builders/vuln/<kind> 实现具体 skill 的 Builder
//   - 两者通过 skill.Builder 类型解耦
package skill

import (
	"context"
	"encoding/json"

	"github.com/V3teran/liusha/internal/llm"
	"github.com/V3teran/liusha/internal/react"
	"github.com/V3teran/liusha/internal/sandbox"
)

// Builder 为某个 skill 装配 ReAct Config。
//
// hunter 小队：scanner 拉到 flow 直接调 hunter.NewBuilder 拿 react.Config 跑 react.Run。
type Builder func(ctx context.Context, params BuilderParams) (react.Config, error)

// BuilderParams hunter agent 启动参数。
//
// 两种模式：
//   - passive: scanner ingestor 拉到 flow 后填 FlowID/URL/Method + Request*/Response*，
//     hunter user prompt 拼完整 raw 流量（请求 + 响应）；Host = 流量真实 host。
//   - active: httpapi /scan/active 入口填 Brief（用户自然语言整段），目标 URL/host/凭据/
//     测试范围全部塞在 brief 里由 LLM 自己识别；Host 由 extractHostFromBrief 先从 brief
//     抽真实 host（cmd/scanner/handler_active.go），抽不到才回退 owner_id 作虚拟 host。
//     notes/findings/lessons 按 (owner, host) 切分。
//
// Mode 决定 builder 内部 user prompt 渲染分支与工具注册（如 active 不挂 read_credentials）。
type BuilderParams struct {
	OwnerType string // 'passive_session' / 'active_scan'
	OwnerID   string // passive_session.id / active_scan.id（也用作 notes/lesson key + finding.owner_id 冗余列）
	TaskID    string
	// CommanderTaskID 非空表示本任务是 commander spawn 的striker（subtask swarm）。
	// commander / 独立任务此字段为空。hunter builder（PR3）按此字段决定是否注册
	// spawn_striker / list_strikers 工具——striker不再 spawn（max_depth=1）。
	CommanderTaskID string
	Host         string
	LLM          llm.Generator
	Inspector     react.Inspector

	// Mode 区分入口形态："passive" | "active"。
	Mode string

	// Passive 模式独有：原始 HTTP 流量（请求 + 响应）。
	FlowID          int64
	URL             string
	Method          string
	RequestHeaders  json.RawMessage
	RequestBody     []byte
	ResponseStatus  int
	ResponseHeaders json.RawMessage
	ResponseBody    []byte

	// Active 模式独有：用户自然语言任务简报（含目标 URL/凭据/测试方向等所有信息）。
	// hunter 把 Brief 整段塞 user prompt，由 LLM 自决目标识别 / 登录方式 / 扫描策略。
	Brief string

	// Sandbox 是本次 agent run 绑定的 sandbox-server HTTP RPC client。
	// 由 cmd/scanner 在 Launcher.Spawn 后填入；run 结束 defer Destroy。
	// hunter Builder 闭包用此 client 注入 RunCommand.Sandbox。
	// nil 时 hunter 不注册 run_command 工具（向后兼容，避免 LLM 调不到工具）。
	Sandbox sandbox.Client
}
