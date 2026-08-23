// Package planner 是认知引擎（L4）的规划环：读世界模型当前态，推导未展开的
// 攻击面（frontier），产出排序后的下一步招法。只看图、不执行、不调 LLM——
// 执行归 Executor，排序策略经 Strategy 接口可插拔。
package planner

import "github.com/V3teran/liusha/internal/worldmodel"

// MoveKind 对齐公有领域杀伤链阶段，domain-agnostic。
type MoveKind string

const (
	MoveEnumerate MoveKind = "enumerate" // 信息收集/枚举（web目录、端口、云资产、域对象、CTF初探）
	MoveProbe     MoveKind = "probe"     // 漏洞探测/弱点发现（扫描/fuzz/初步PoC，不坐实）
	MoveExploit   MoveKind = "exploit"   // 漏洞利用/坐实（PoC确认、初始立足点获取）
	MoveEscalate  MoveKind = "escalate"  // 权限提升/横向移动（本机提权/域提权/云IAM/跨主机）
	MovePersist   MoveKind = "persist"   // 后渗透（数据采集/持久化/C2/影响评估）
)

// Move 是派发给 Executor 的一步招法，不是 L1 原语；原语由 Executor 执行时定。
type Move struct {
	Kind     MoveKind
	Target   worldmodel.TargetRef
	OnNodeID string // frontier 锚点节点
	Reason   string // 可审计：进报告与 trace
	Priority int    // Strategy 赋值，越大越先
}
