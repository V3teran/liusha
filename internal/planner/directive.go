package planner

import (
	"fmt"
	"strings"
)

var kindDirective = map[MoveKind]string{
	MoveEnumerate: "枚举此目标，探明其攻击面（服务、端口、端点、资产、域对象、云资源）。",
	MoveProbe:     "探测此资产弱点，扫描/fuzz 疑似漏洞，产出线索不坐实。",
	MoveExploit:   "针对此弱点坐实利用，验证 PoC，获取初始立足点。",
	MoveEscalate:  "自此立足点扩展攻击面：提权（本机/域/云IAM）或横向移动（跨主机/跨服务）。",
	MovePersist:   "自此立足点采集数据、建立持久化、评估影响范围。",
}

// Directive 把招法渲染成前置到 agent user prompt 的中文指令块。
// 纯函数、无副作用（对齐 planner「只看图、不执行」的边界）；零值 Move（Kind 为空）
// 返回空串，让上层退回静态 prompt。
func (m Move) Directive() string {
	if m.Kind == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("## 本步作战招法（L4 规划器指派）\n")
	fmt.Fprintf(&b, "- 阶段：%s\n", m.Kind)
	if d := kindDirective[m.Kind]; d != "" {
		fmt.Fprintf(&b, "- 指向：%s\n", d)
	}
	if loc := m.Target.Locator; loc != "" {
		fmt.Fprintf(&b, "- 锚点：%s（%s/%s）\n", loc, m.Target.Domain, m.Target.RefKind)
	}
	if m.Reason != "" {
		fmt.Fprintf(&b, "- 因由：%s\n", m.Reason)
	}
	b.WriteString("\n请聚焦此招法行动；招法之外的机会照常记录，但不必在本步展开。\n")
	return b.String()
}
