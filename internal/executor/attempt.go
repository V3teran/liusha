package executor

import (
	"encoding/json"
	"strings"

	"github.com/V3teran/liusha/internal/evaluator"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/framework/core"
)

// findingContent 是 finding 晋升成探索图 discovery 节点时写入 Content 的漏洞元数据。
// 回指 finding_id/seq 闭合「图节点 ↔ finding 记录」双向溯源。
type findingContent struct {
	Type      string                     `json:"type"` // "vulnerability"
	FindingID string                     `json:"finding_id"`
	Seq       int64                      `json:"seq,omitempty"`
	Severity  string                     `json:"severity,omitempty"`
	Summary   string                     `json:"summary"`
	CWEID     string                     `json:"cwe_id,omitempty"`
	OWASP     string                     `json:"owasp_category,omitempty"`
	TargetRef explorationgraph.TargetRef `json:"target_ref"`
}

// AttemptFromFinding 把一条 web finding 翻译成 evaluator.Attempt（提议权兑现：报告 → 待裁决晋升）。
//
// 返回 (attempt, true, nil) 表示可晋升；(_, false, nil) 表示该 finding 无复现配方，
// 不进复现门（只留人读记录，铁律：图只存能坐实的态）。taskID 承接跨轴映射——
// finding 挂 task 轴，探索图图挂 scan(assignment) 轴，由调用方传入图归属。
func AttemptFromFinding(taskID string, f finding.VulnFinding) (evaluator.Attempt, bool, error) {
	// 无复现配方即不可晋升：Verifier 无从复现，跳过（不是错误——多数存量 finding 如此）。
	if len(f.Repro) == 0 || string(f.Repro) == "{}" {
		return evaluator.Attempt{}, false, nil
	}

	// 已坐实的 finding（evaluator 写入，evaluation 带 verification_id）不回晋升门：
	// 否则 executor 的 before/after 快照会把它当新 finding 收割 → 再验证 → 再写 finding，
	// 形成无限复验循环。复现门的产物只进图，不回流。
	if findingAlreadyVerified(f.Evaluation) {
		return evaluator.Attempt{}, false, nil
	}

	targetRef := endpointRef(f)

	content, err := json.Marshal(findingContent{
		Type:      "vulnerability",
		FindingID: f.ID,
		Seq:       f.Seq,
		Severity:  f.Severity,
		Summary:   f.Summary,
		CWEID:     f.CWEID,
		OWASP:     f.OWASPCategory,
		TargetRef: targetRef,
	})
	if err != nil {
		return evaluator.Attempt{}, false, err
	}

	// 计算优先级：severity 映射
	priority := severityToPriority(f.Severity)

	return evaluator.Attempt{
		TaskID:     taskID,
		Kind:       core.KindResult, // 漏洞是重要发现
		Primitives: f.Repro,         // 形状已是 ReplayRecipe，Verifier 侧 web.Replayer 解析
		Content:    content,
		Priority:   priority,
	}, true, nil
}

// findingAlreadyVerified 判断 finding 是否复现门的产物（evaluator 坐实后写入，
// evaluation 携带 verification_id 回指审计链）。
func findingAlreadyVerified(evaluation json.RawMessage) bool {
	if len(evaluation) == 0 {
		return false
	}
	var e struct {
		VerificationID string `json:"verification_id"`
	}
	if err := json.Unmarshal(evaluation, &e); err != nil {
		return false
	}
	return e.VerificationID != ""
}

// endpointRef 从 finding 派生 web endpoint 的多态目标 ref（locator = host+path）。
// path 从 finding.Target.{path} 取；缺则退化为纯 host（站点粒度）。
func endpointRef(f finding.VulnFinding) explorationgraph.TargetRef {
	locator := f.Host
	if p := targetPath(f.Target); p != "" {
		locator = strings.TrimRight(f.Host, "/") + ensureObservationingSlash(p)
	}
	return explorationgraph.TargetRef{Domain: "web", RefKind: "endpoint", Locator: locator}
}

// targetPath 从 finding.Target（自由 object，惯例含 path）抽 path；解析失败/无 path 返空。
func targetPath(target json.RawMessage) string {
	if len(target) == 0 {
		return ""
	}
	var t struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(target, &t); err != nil {
		return ""
	}
	return t.Path
}

func ensureObservationingSlash(p string) string {
	if strings.HasPrefix(p, "/") {
		return p
	}
	return "/" + p
}

// severityToPriority 将 severity 映射到优先级（统一走 explorationgraph 归一）。
func severityToPriority(severity string) string {
	return string(explorationgraph.PriorityFromSeverity(severity))
}
