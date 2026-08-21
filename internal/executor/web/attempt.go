package web

import (
	"encoding/json"
	"strings"

	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/verifier"
	"github.com/V3teran/liusha/internal/worldmodel"
)

// findingAttrs 是 finding 晋升成世界模型 KindFinding 节点时写入 Attrs 的漏洞元数据。
// 回指 finding_id/seq 闭合「图节点 ↔ finding 记录」双向溯源。
type findingAttrs struct {
	FindingID string `json:"finding_id"`
	Seq       int64  `json:"seq,omitempty"`
	Severity  string `json:"severity,omitempty"`
	Summary   string `json:"summary"`
	CWEID     string `json:"cwe_id,omitempty"`
	OWASP     string `json:"owasp_category,omitempty"`
}

// AttemptFromFinding 把一条 web finding 翻译成 verifier.Attempt（提议权兑现：报告 → 待裁决晋升）。
//
// 返回 (attempt, true, nil) 表示可晋升；(_, false, nil) 表示该 finding 无复现配方，
// 不进复现门（只留人读记录，铁律：图只存能坐实的态）。taskID 承接跨轴映射——
// finding 挂 task 轴，世界模型图挂 scan(assignment) 轴，由调用方传入图归属。
func AttemptFromFinding(taskID string, f finding.VulnFinding) (verifier.Attempt, bool, error) {
	// 无复现配方即不可晋升：Verifier 无从复现，跳过（不是错误——多数存量 finding 如此）。
	if len(f.Repro) == 0 || string(f.Repro) == "{}" {
		return verifier.Attempt{}, false, nil
	}

	attrs, err := json.Marshal(findingAttrs{
		FindingID: f.ID,
		Seq:       f.Seq,
		Severity:  f.Severity,
		Summary:   f.Summary,
		CWEID:     f.CWEID,
		OWASP:     f.OWASPCategory,
	})
	if err != nil {
		return verifier.Attempt{}, false, err
	}

	return verifier.Attempt{
		ScanID:     taskID,
		Kind:       worldmodel.KindFinding,
		Target:     endpointRef(f),
		Primitives: f.Repro, // 形状已是 ReplayRecipe，Verifier 侧 web.Replayer 解析
		Attrs:      attrs,
	}, true, nil
}

// endpointRef 从 finding 派生 web endpoint 的多态目标 ref（locator = host+path）。
// path 从 finding.Target.{path} 取；缺则退化为纯 host（站点粒度）。
func endpointRef(f finding.VulnFinding) worldmodel.TargetRef {
	locator := f.Host
	if p := targetPath(f.Target); p != "" {
		locator = strings.TrimRight(f.Host, "/") + ensureLeadingSlash(p)
	}
	return worldmodel.TargetRef{Domain: "web", RefKind: "endpoint", Locator: locator}
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

func ensureLeadingSlash(p string) string {
	if strings.HasPrefix(p, "/") {
		return p
	}
	return "/" + p
}
