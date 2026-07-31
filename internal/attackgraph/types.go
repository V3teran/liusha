// Package attackgraph 把一次扫描的执行轨迹投影成一张「语义调查图」。
//
// 设计见 docs/attack-graph-design.md：图是 read-model 投影，不落任何表；
// 节点/边从现有源表（message 事件流 + finding 等）实时派生。
//
// 与旧版（事件 1:1 投影成时序树）的根本区别：本版按「语义单元」建模，
// 一次扫描的几百上千条事件塌缩成几十个语义节点，让人一眼读出
// 「任务 → 判断 → 探测 → 信号 → 漏洞」这条调查故事线。
//
// 本文件只定义图的输出类型，无 IO、无 DB 依赖，便于纯函数测试。
package attackgraph

// 节点种类（5 类语义维度，见设计 §5）。
//
// 每类回答一个观察者关心的问题：
//   - task       谁在查哪条线（orchestrator 根 + 每个 spawn 子任务）
//   - hypothesis LLM 怎么想的（被提炼的关键推理/决策）
//   - probe      怎么挖的（服务同一判断的连续工具调用聚合）
//   - signal     关键发现·线索（被提炼的关键观察，含「此路不通」）
//   - finding    关键发现·终点（确认的漏洞）
const (
	KindTask       = "task"       // 任务：orchestrator 根 + 每个 spawn 子任务
	KindHypothesis = "hypothesis" // 判断（想）：被提炼的关键推理/决策
	KindProbe      = "probe"      // 探测（做）：服务同一判断的工具调用聚合
	KindSignal     = "signal"     // 信号（得）：被提炼的关键观察（含负面结果）
	KindFinding    = "finding"    // 漏洞：引用 finding 实体
)

// 边类型（7 类，有语义方向；替代旧版唯一的 flow 时序边）。
//
// 一条完整故事线：
//
//	task ─spawns→ 子task
//	task ─pursues→ hypothesis ─tests→ probe ─reveals→ signal ─confirms→ finding
//	                   ▲                                  │
//	                   └──────────── informs ────────────┘
//	                        （信号引出下一个判断 = 调查回环）
const (
	EdgeSpawns    = "spawns"     // 任务 → 子任务（派生子代理）
	EdgePursues   = "pursues"    // 任务 → 判断（这条线在追哪个猜想）
	EdgeTests     = "tests"      // 判断 → 探测（为验证判断做的事）
	EdgeReveals   = "reveals"    // 探测 → 信号（探测揭示了什么观察）
	EdgeInforms   = "informs"    // 信号 → 判断（发现引出下一个判断，调查回环）
	EdgeConfirms  = "confirms"   // 信号/探测 → 漏洞（证据证实了漏洞）
	EdgeDependsOn = "depends_on" // 漏洞 → 漏洞（组合依赖）
)

// 节点来源（provenance）：这个语义节点是怎么产生的。
// 供前端标注可信度、也便于调试提炼质量。
const (
	ProvDerived = "derived" // 确定性派生（task/probe/finding：零歧义）
	ProvAgent   = "agent"   // agent 自标（mark_insight 工具，最可靠）
	ProvLLM     = "llm"     // LLM 事后提炼（无 agent 自标时的兜底，主要是历史数据）
)

// Status 取值（按 Kind 分别适用）。
const (
	// probe（探测）
	StatusRunning = "running" // 执行中（tool_call 已发、result 未回）
	StatusDone    = "done"    // 完成
	StatusFailed  = "failed"  // 失败（死路信号）
	// hypothesis（判断）
	StatusOpen      = "open"      // 待验证
	StatusConfirmed = "confirmed" // 已证实
	StatusRefuted   = "refuted"   // 已否定
	// task（任务）：running / done
)

// Node 是图节点。原文不入节点（图存决策，不存证据）：
// 节点只带短标签 Title + 指向原文的指针 Ref，点开取完整原文。
type Node struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	ParentID string `json:"parent_id,omitempty"` // 骨干树父节点（供 markOnPath 上溯与布局）
	Agent    string `json:"agent,omitempty"`     // 产出该节点的代理（orchestrator/exploitation/…），前端按它分组/配色
	Title    string `json:"title"`               // 短标签（任务目标 / 判断摘要 / 工具名 / 信号摘要 / 漏洞标题）
	Ref      string `json:"ref,omitempty"`       // 指针：finding id / message id，点开取原文
	Severity string `json:"severity,omitempty"`  // 漏洞节点配色用
	Status   string `json:"status,omitempty"`    // 见上方 Status 常量（按 Kind 适用）
	OnPath   bool   `json:"on_path"`             // 成果路径：通向某 finding（默认展开）；false=探索/死路，默认折叠

	Provenance string `json:"provenance,omitempty"` // derived / agent / llm
	Host       string `json:"host,omitempty"`       // 漏洞所属站点（原 Target，老实透传，不再假装是切片键）
	DurationMs int    `json:"duration_ms,omitempty"` // probe 执行耗时（原本落库却丢弃，现透传）
	Tokens     int    `json:"tokens,omitempty"`      // hypothesis 对应推理的 LLM token 数（in+out）
}

// Edge 是有向边。
type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Type string `json:"type"`
}

// CollapsedSegment 是一段被折叠的探索噪声的元数据（由后端产出，前端只管展开/收起）。
//
// 旧版让前端用 nearestKept/hiddenCount 自己重算折叠（复刻后端已有的图知识、且是分歧风险源）。
// 现在后端投影时直接算好：哪个可见节点（Anchor）名下折叠了几步（HiddenCount），
// 前端拿到就画一个占位节点，点击 toggle 展开。
type CollapsedSegment struct {
	Anchor      string `json:"anchor"`       // 折叠段挂靠的可见节点 id（空=开场段，挂在根前）
	HiddenCount int    `json:"hidden_count"` // 该段折叠了多少个探索节点
	Opening     bool   `json:"opening"`      // 是否开场段（侦察与初始访问）
}

// Graph 是投影结果（read-model，不落表）。
type Graph struct {
	TaskID string `json:"task_id"`
	// ConversationID 是本图来源的会话 id（后端按 task 自解析回传）。
	// 前端据此钻取原文 / 拉里程碑，无需自己 join 会话列表。空=无绑定会话。
	ConversationID string `json:"conversation_id"`
	// Running 表示该 task 是否仍在扫描中（权威：task 终态）。前端据此决定是否订阅 SSE。
	Running bool `json:"running"`
	// Enriched 表示第二趟 LLM 语义提炼是否已完成。
	// 实时阶段只出确定性骨架 + agent 自标（Enriched=false，流畅不烧钱）；
	// 扫描结束/手动刷新补 LLM 提炼后 Enriched=true（完整语义图）。
	Enriched bool               `json:"enriched"`
	Nodes    []Node             `json:"nodes"`
	Edges    []Edge             `json:"edges"`
	Collapsed []CollapsedSegment `json:"collapsed,omitempty"` // 折叠段元数据（成果优先视图）
}
