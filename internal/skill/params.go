package skill

import (
	"encoding/json"

	"github.com/V3teran/liusha/internal/sandbox"
	"github.com/V3teran/liusha/internal/traffic"
)

// BuilderParams 是 hunter agent 的启动参数（与 SKILL.md Loader 同包）。
//
// 两种模式：
//   - passive: runner ingestor 拉到 flow 后填 FlowID/URL/Method + Request*/Response*，
//     hunter user prompt 拼完整 raw 流量（请求 + 响应）；Host = 流量真实 host。
//   - active: 会话/API 入口填 Brief（用户自然语言整段），目标 URL/host/凭据/范围全塞 brief
//     由 LLM 自识别；Host 由 extractHostFromBrief 先从 brief 抽真实 host，抽不到回退 task_id。
//     findings/lessons 按 (task/host) 切分。
//
// eino 路径用此结构传给 hunter.BuildUserPrompt 拼 user prompt（react 退路已删）。
type BuilderParams struct {
	TaskID   string // 所属 task.id（也用作 finding/lesson 归属）
	HunterID string
	// OrchestratorID 非空表示本任务是子任务（旧 subtask swarm 语义）；deep 临时 sub-agent 不建
	// 独立 hunter，active 主任务此字段为空。保留供 BuildUserPrompt 兼容。
	OrchestratorID string
	Host           string

	// 单条 HTTP 流量（请求 + 响应）的可选上下文。
	FlowID          int64
	URL             string
	Method          string
	RequestHeaders  json.RawMessage
	RequestBody     []byte
	ResponseStatus  int
	ResponseHeaders json.RawMessage
	ResponseBody    []byte

	// Passive 批分析：本 task 认领的整批 proxy_traffic（consumed_by_task_id=本 task），
	// handler 全读后填入，BuildUserPrompt 全量渲染成流量清单（摘要 + body 预览）推进 prompt——
	// agent 开箱即见本批全部流量，不必靠 list_traffic 发现；需完整 body 才调 view_traffic。
	Flows []traffic.ProxyTraffic

	// Active 模式独有：用户自然语言任务简报（含目标 URL/凭据/测试方向等所有信息）。
	Brief string

	// CliTools 是本猎手的外置 CLI 工具集（tools.yaml 名字），来自 hunter.CliTools。
	// buildToolingCatalog 经 manifest.FilterByNames 严格白名单过滤：
	// 空 = 空集（不装配任何外部工具）；非空 = 只渲染白名单内工具（猎手专精，只给它这几把刀）。
	CliTools []string

	// Sandbox 是本次 agent run 绑定的 sandbox-server HTTP RPC client（handler Spawn 后填）。
	// nil 时不注册 run_command 工具。
	Sandbox sandbox.Client

	// FindingsLimit 是 user prompt「该 host 已有 finding」段的显示条数上限；≤0 → 100。
	// 由 handler 每次装 prompt 时从 settingstore(runtime 组) 现读注入，DB 改即生效（不再启动烘焙）。
	FindingsLimit int
}
