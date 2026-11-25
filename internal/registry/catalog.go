package registry

// FunctionToolCategory 是内置函数工具的功能域分类（展示分组用）。
type FunctionToolCategory string

// 工具目录分类（智能体页工具装配、工具页分组展示用）。
const (
	CatCredentials FunctionToolCategory = "credentials"
	CatCorpus      FunctionToolCategory = "corpus"
	CatWebSearch   FunctionToolCategory = "web_search"
	CatTraffic     FunctionToolCategory = "traffic"
	CatSandbox     FunctionToolCategory = "sandbox"
	CatSkill       FunctionToolCategory = "skill"
	CatExplore     FunctionToolCategory = "explore"
	CatControl     FunctionToolCategory = "control"
)

// Name 返回分类的中文显示名（工具目录同步入库，工具页 / 智能体页据此展示）。
func (c FunctionToolCategory) Name() string {
	switch c {
	case CatCredentials:
		return "凭证共享"
	case CatCorpus:
		return "知识库"
	case CatWebSearch:
		return "网页检索"
	case CatTraffic:
		return "流量分析"
	case CatSandbox:
		return "沙箱执行"
	case CatSkill:
		return "技能手册"
	case CatExplore:
		return "探索图"
	case CatControl:
		return "流程控制"
	}
	return string(c)
}

// FunctionToolMeta 是一个内置函数工具的展示元数据。
type FunctionToolMeta struct {
	Name        string
	Category    FunctionToolCategory
	Description string
}

// FunctionToolCatalog 是全部内置函数工具的声明式清单（供工具目录入库同步）。
// 名字集合须与 tools.BuildTools 实际注册集严格双向一致（catalog_test 防漂移）；
// evaluator 包内注册的 repro_replay 也在册（judge 装配用）。
var FunctionToolCatalog = []FunctionToolMeta{
	{"credential_read", CatCredentials, "读取本 host 预录入的真实身份（cookie/token/body 字段），可直接拼请求做重放/越权测试。"},
	{"credential_write", CatCredentials, "把刚拿到的活凭证录入本 host 凭证池，供同 task 下其他执行者共享复用。"},
	{"corpus_search", CatCorpus, "检索跨任务长期知识库：以往任务沉淀与人工编写的方法、经验与教训，按相关度过滤。"},
	{"web_search", CatWebSearch, "检索公开网页：返回与查询相关的网页（标题、地址、内容摘要）。接入方法见 config.yaml 的 web_search 段。"},
	{"traffic_list", CatTraffic, "列出本次扫描范围内的历史 HTTP 流量摘要，可按 method/path/status 等过滤。"},
	{"traffic_view", CatTraffic, "取单条历史流量的完整 raw HTTP 请求+响应（headers/body 全量）。"},
	{"http_request", CatTraffic, "发完整 HTTP 请求（自动注入凭证库身份、保留会话 cookie、抓流落库），漏洞复现配方的采集前端。"},
	{"command_run", CatSandbox, "在沙箱内执行完整 shell 命令（管道/重定向/环境变量），跑外部 CLI 工具。"},
	{"browser_drive", CatSandbox, "用真实 chromium 浏览器操作目标页面（登录/点击/读 DOM/跑 JS），复用登录态。"},
	{"skill_read", CatSkill, "拉取一个 skill 的完整手册正文（可用名单见 system prompt 的技能索引段）。"},
	{"repro_replay", CatExplore, "重放复现配方取得机器证据（评估者专用，绑定当前假设）。"},
	{"graph_read", CatExplore, "深挖探索图当前状态（节点/边/依赖细节，planner 规划用）。"},
	{"progress_evaluate", CatExplore, "汇总任务进展：动作按状态计数、完成率与已坐实的结果数（planner 规划用）。"},
	{"task_survey", CatExplore, "获取任务全局状态快照（monitor 监察用）。"},
	{"decision_publish", CatExplore, "发布监察决策：否决动作 reject_action（带 action_id；用户指派的动作不可否决）或请求重规划 request_replan（monitor 专用）。"},
}
