package registry

// FunctionToolCategory 是内置函数工具的功能域分类（展示分组用）。
type FunctionToolCategory string

const (
	CatFindings    FunctionToolCategory = "findings"
	CatCredentials FunctionToolCategory = "credentials"
	CatCorpus      FunctionToolCategory = "corpus"
	CatLead        FunctionToolCategory = "lead"
	CatTraffic     FunctionToolCategory = "traffic"
	CatSandbox     FunctionToolCategory = "sandbox"
	CatSkill       FunctionToolCategory = "skill"
	CatControl     FunctionToolCategory = "control"
)

// FunctionToolMeta 是一个内置函数工具的展示元数据。
type FunctionToolMeta struct {
	Name        string
	Category    FunctionToolCategory
	Description string
}

// FunctionToolCatalog 是全部内置函数工具的声明式清单（供工具目录入库同步）。
// 名字集合须与 registry 注册表 KnownToolNames() 严格双向一致（catalog_test 防漂移）。
var FunctionToolCatalog = []FunctionToolMeta{
	{"read_credentials", CatCredentials, "读取本 host 预录入的真实身份（cookie/token/body 字段），可直接拼请求做重放/越权测试。"},
	{"write_credential", CatCredentials, "把刚拿到的活凭证录入本 host 凭证池，供同 owner 下其他 executor 共享复用。"},
	{"read_findings", CatFindings, "列出本次扫描已有 finding（写前必查、防重复），返回 id/severity/summary 摘要。"},
	{"write_finding", CatFindings, "写一条新漏洞 finding：summary 短标题 + evidence 详情/复现/payload + severity。"},
	{"update_finding", CatFindings, "更新已有 finding（保留首次发现时间，仅覆盖所传字段），用于补强 PoC/payload/severity。"},
	{"search_corpus", CatCorpus, "检索跨目标长期知识库：沉淀的可复用打法、专家经验、历史教训。"},
	{"write_corpus", CatCorpus, "向跨目标长期知识库沉淀一条可复用知识（有质量门槛，防噪音）。"},
	{"write_lead", CatLead, "写一条跨 agent 情报到情报黑板（按 host 共享给子代理/跨 run，不进交付报告）。"},
	{"done", CatControl, "终止当前任务收尾，可带 reason/summary 供检查器与报告参考。"},
	{"mark_insight", CatControl, "在执行图上标记关键节点（判断/发现），帮观察者看懂调查思路，不进黑板。"},
	{"replay_traffic", CatTraffic, "重发历史 HTTP 流量并可字段级改写，做越权/未授权/IDOR/fuzz，自动保留 session 上下文。"},
	{"list_traffic", CatTraffic, "列出本次扫描范围内的历史 HTTP 流量摘要，可按 method/path/status 等过滤。"},
	{"view_traffic", CatTraffic, "取单条历史流量的完整 raw HTTP 请求+响应（headers/body 全量）。"},
	{"run_command", CatSandbox, "在沙箱内执行完整 shell 命令（管道/重定向/环境变量），跑外部 CLI 工具。"},
	{"browser_use", CatSandbox, "用真实 chromium 浏览器操作目标页面（登录/点击/读 DOM/跑 JS），复用登录态。"},
	{"read_tooling_skill", CatSkill, "拉取某个外部 CLI 工具的完整使用手册。"},
	{"read_vuln_skill", CatSkill, "拉取某个漏洞类型的完整挖掘指南。"},
}
