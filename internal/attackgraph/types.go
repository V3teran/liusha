// Package attackgraph 把一次扫描的执行轨迹与漏洞成果投影成一张图（思维链 + 成果链）。
//
// 设计见 docs/attack-graph-design.md：图是 read-model 投影，不落任何表；
// 节点/边从现有源表（message 事件流 + finding 等）实时派生。
//
// 本文件只定义图的输出类型，无 IO、无 DB 依赖，便于纯函数测试。
package attackgraph

// 节点种类（见设计 §5）。
const (
	KindReasoning = "reasoning" // 想：一次模型调用的 CoT（含中间猜测）
	KindAction    = "action"    // 做+得：一次工具调用（args=做、output=得）
	KindFinding   = "finding"   // 漏洞：引用 finding 实体
	KindAgent     = "agent"     // 子代理边界（orchestrator → 子代理）
)

// 边类型（见设计 §5）。
const (
	EdgeFlow      = "flow"       // 思维链骨干：父子 / 时序
	EdgeDependsOn = "depends_on" // 成果链：漏洞间组合依赖
	EdgeEvidence  = "evidence"   // 漏洞 → 证据（flow / action）
)

// Node 是图节点。原文不入节点（见设计 §6：图存决策，不存证据），
// 节点只带代码生成的短标签 Title + 指向原文的指针 Ref。
type Node struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	ParentID string `json:"parent_id,omitempty"` // 思维链骨干树的父节点
	Agent    string `json:"agent,omitempty"`     // 产出该节点的子代理（orchestrator/exploitation/…），前端按它分组/配色
	Target   string `json:"target,omitempty"`    // 所属站（per-host 切片键），可空=全局
	Title    string `json:"title"`               // 短标签（首行 / 工具名 / 漏洞摘要）
	Ref      string `json:"ref,omitempty"`       // 指针：finding id / message id，点开取原文
	Severity string `json:"severity,omitempty"`  // 漏洞节点配色用
	Status   string `json:"status,omitempty"`    // 动作节点：done / error（实时态另有 running）
	OnPath   bool   `json:"on_path"`             // 成果路径：通向某 finding（前端「成果优先」默认展开）；false=死路/探索，默认折叠
}

// Edge 是有向边。
type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Type string `json:"type"`
}

// Graph 是投影结果（read-model，不落表）。
type Graph struct {
	TaskID string `json:"task_id"`
	Nodes  []Node `json:"nodes"`
	Edges  []Edge `json:"edges"`
}
