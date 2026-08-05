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

	// Domain 是当次 scenario 的交战域（web/ctf/cloud…），来自 scenario.Domain。
	// buildToolingCatalog 据此经 manifest.FilterByDomain 过滤 CLI 扫描工具目录（见 D11/M7）；
	// 空串 = 场景未配域，不过滤（全集渲染）。
	Domain string

	// CliTools 是本猎手的外置 CLI 工具白名单（tools.yaml 名字），来自 hunter.CliTools。
	// buildToolingCatalog 在 FilterByDomain 之上再经 manifest.FilterByNames 二级细过滤：
	// 空 = 不细过滤（域内全部可见）；非空 = 只渲染白名单内工具（猎手专精，只给它这几把刀）。
	CliTools []string

	// Sandbox 是本次 agent run 绑定的 sandbox-server HTTP RPC client（handler Spawn 后填）。
	// nil 时不注册 run_command 工具。
	Sandbox sandbox.Client
}
