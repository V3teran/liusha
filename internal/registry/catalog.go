package registry

// FunctionToolCategory 是内置函数工具的功能域分类（展示分组用）。
type FunctionToolCategory string

// 工具目录分类（AgentAdmin 装配页分组展示用）。
const (
	CatCredentials FunctionToolCategory = "credentials"
	CatCorpus      FunctionToolCategory = "corpus"
	CatInsight     FunctionToolCategory = "insight"
	CatTraffic     FunctionToolCategory = "traffic"
	CatSandbox     FunctionToolCategory = "sandbox"
	CatSkill       FunctionToolCategory = "skill"
	CatExplore     FunctionToolCategory = "explore"
	CatControl     FunctionToolCategory = "control"
)

// FunctionToolMeta 是一个内置函数工具的展示元数据。
type FunctionToolMeta struct {
	Name        string
	Category    FunctionToolCategory
	Description string
}

// FunctionToolCatalog 是全部内置函数工具的声明式清单（供工具目录入库同步）。
// 名字集合须与 tools.BuildTools 实际注册集严格双向一致（catalog_test 防漂移）；
// evaluator 包内注册的 replay_for_verification 也在册（judge 装配用）。
var FunctionToolCatalog = []FunctionToolMeta{
	{"read_credentials", CatCredentials, "读取本 host 预录入的真实身份（cookie/token/body 字段），可直接拼请求做重放/越权测试。"},
	{"write_credential", CatCredentials, "把刚拿到的活凭证录入本 host 凭证池，供同 owner 下其他 executor 共享复用。"},
	{"search_corpus", CatCorpus, "检索跨目标长期知识库：沉淀的可复用打法、专家经验、历史教训。"},
	{"read_insights", CatInsight, "读取情报黑板：其他执行者共享的情报（按 assignment 隔离，按类别/优先级过滤）。"},
	{"write_insight", CatInsight, "写一条跨 agent 情报到情报黑板（按 host 共享给子代理/跨 run，不进交付报告）。"},
	{"list_traffic", CatTraffic, "列出本次扫描范围内的历史 HTTP 流量摘要，可按 method/path/status 等过滤。"},
	{"view_traffic", CatTraffic, "取单条历史流量的完整 raw HTTP 请求+响应（headers/body 全量）。"},
	{"http_request", CatTraffic, "发完整 HTTP 请求（自动注入凭证库身份、保留会话 cookie、抓流落库），漏洞复现配方的采集前端。"},
	{"run_command", CatSandbox, "在沙箱内执行完整 shell 命令（管道/重定向/环境变量），跑外部 CLI 工具。"},
	{"drive_browser", CatSandbox, "用真实 chromium 浏览器操作目标页面（登录/点击/读 DOM/跑 JS），复用登录态。"},
	{"read_skill", CatSkill, "拉取一个 skill 的完整手册正文（可用名单见 system prompt 的技能索引段）。"},
	{"write_observation", CatExplore, "向探索图写 Observation 节点：假设 + 证据 + 机器可复现配方（repro），晋升门的入口。"},
	{"replay_for_verification", CatExplore, "重放复现配方取得机器证据（evaluator 裁决专用，绑定当前假设）。"},
	{"observe_state", CatExplore, "深挖探索图当前状态（节点/边/依赖细节，planner 规划用）。"},
	{"evaluate_progress", CatExplore, "探索图全局进展评估：完成率/失败分布/停滞检测（planner 规划用）。"},
	{"get_global_state", CatExplore, "获取任务全局状态快照（monitor 监察用）。"},
	{"publish_decision", CatExplore, "发布监察决策：kill_action（带 action_id）或 request_replan（monitor 专用）。"},
}
