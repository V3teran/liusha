// Package finding 实现 finding 表持久化层：漏洞台账。
//
// 一条漏洞对应一个坐实结果节点（ResultNodeID）：交付按结果节点幂等。主张的语义去重不在
// 这里——复现门按主张指纹在探索图里把同一主张的再次确认折叠进同一结果节点，台账与结果
// 一一对应。
//
// 台账是坐实主张的投影，由 ClaimSink 唯一写入：主张坐实了就必须存得下，行里各列的取值域
// 覆盖主张的全部合法取值（词表类的列在交付处收进词表，自由文本的列不加约束）。
package finding

import (
	"encoding/json"
	"time"
)

// Finding 是 finding 表行的 Go 表示。
//
// Target / Evaluation 为 nil 时 Save 自动落空对象 '{}'。
type Finding struct {
	ID           string
	TaskID       string // 所属 task.id
	ResultNodeID string // 坐实这条漏洞的探索图结果节点（必填；交付幂等键）
	AgentRunID   *string
	// Host 是发现所在的主机（host[:port]）。坐实材料与任务目标里都定位不到主机时为空——
	// 空是如实的「未知」，不是漏填。
	Host string
	// Severity 是 Severities 五档之一（DB CHECK 约束 finding_severity_check）；交付缺省 medium。
	Severity string
	// Summary 是坐实主张的陈述原文：发现是什么、怎么验证。不限长度与换行，评估理由另在
	// Evaluation 里。Store.Save 强制非空。
	Summary    string
	Target     json.RawMessage
	Evaluation json.RawMessage
	// Repro 是通过复现门的配方（信封 {kind,recipe,assert}），交付时随主张落库，供复验
	// 与人读。recipe/assert 的形状归配方形状，本层只作 jsonb 透传。
	Repro json.RawMessage
	// Class 是发现的类型，取自封闭词表（class.go）：坐实主张时评估者给出的主张类别，词表之外的
	// 与未给出的归入 other。展示名与 CWE 按类型从词表取。
	Class       string
	Remediation string // 修复建议自然语言；evidence 仍存 PoC

	// —— triage 处置字段 ——
	// Status 是处置态：open/confirmed/fixed/false_positive/accepted（DB CHECK 约束五态）。
	// 落库默认 open（DB 列默认值）；人工在漏洞页 triage 流转。
	Status string
	// TriageNote 处置备注（自由文本，如误报原因）；未处置为空。
	TriageNote string
	// TriagedAt 最后状态变更时刻；open（从未处置）为 nil。
	TriagedAt *time.Time

	// Seq 是对外顺序号（bigserial，单调递增，跨 task/host 唯一）；报告/会话引用用。
	// 内部主键仍是 ID(uuid)；Seq 仅作人可读短标识（类 GitHub #123）。
	Seq int64

	CreatedAt time.Time
}
