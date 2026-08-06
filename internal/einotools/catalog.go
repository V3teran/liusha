package einotools

// catalog.go：内置函数工具（function tools）的声明式元数据目录。
//
// 为什么需要这一层：各工具的描述埋在 Build* 里、且取 Info 需运行期依赖（Sandbox/store），
// 无法零依赖反射。故在此以声明式清单集中登记「名字→分类+展示描述」，供工具目录入库/展示。
// 这是唯一的手工维护点——catalog_test.go 断言本清单的名字集合与注册表 KnownToolNames() 严格
// 双向一致，新增/删工具而漏改此处即测试失败，防漂移。
//
// 描述取「展示友好的一行简述」，非喂 LLM 的完整 prompt（后者仍在各 Build* 内，随运行期动态拼接）。

// FunctionToolCategory 是内置工具的功能域分类（展示分组用）。
type FunctionToolCategory string

const (
	CatFindings    FunctionToolCategory = "findings"    // 漏洞记录读写
	CatCredentials FunctionToolCategory = "credentials" // 凭证池共享
	CatCorpus      FunctionToolCategory = "corpus"      // 跨目标长期知识库
	CatLead        FunctionToolCategory = "lead"        // 跨 agent 情报黑板
	CatTraffic     FunctionToolCategory = "traffic"     // HTTP 流量列举/查看/重放
	CatSandbox     FunctionToolCategory = "sandbox"     // 沙箱内命令/浏览器执行
	CatSkill       FunctionToolCategory = "skill"       // 技能手册索引
	CatControl     FunctionToolCategory = "control"     // 流程控制/执行图标记
)

// FunctionToolMeta 是一个内置函数工具的展示元数据。
type FunctionToolMeta struct {
	Name        string
	Category    FunctionToolCategory
	Description string
}

// FunctionToolCatalog 是全部内置函数工具的元数据清单（供入库同步）。
// 名字集合必须与 einoagent 注册表严格一致（catalog_test 防漂移）。
var FunctionToolCatalog = []FunctionToolMeta{
	{"read_credentials", CatCredentials, "读取本 host 预录入的真实身份（cookie/token/body 字段），可直接拼请求做重放/越权测试。"},
	{"write_credential", CatCredentials, "把刚拿到的活凭证录入本 host 凭证池，供同 owner 下其他 hunter 共享复用。"},
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
	{"run_command", CatSandbox, "在沙箱内执行完整 shell 命令（管道/重定向/环境变量），跑 sqlmap/nuclei 等外部 CLI 工具。"},
	{"browser_use", CatSandbox, "用真实 chromium 浏览器操作目标页面（登录/点击/读 DOM/跑 JS），复用登录态。"},
	{"read_tooling_skill", CatSkill, "拉取某个外部 CLI 工具的完整使用手册。"},
	{"read_vuln_skill", CatSkill, "拉取某个漏洞类型的完整挖掘指南。"},
}
