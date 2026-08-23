package binary

import (
	"encoding/json"
	"strings"

	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/verifier"
	"github.com/V3teran/liusha/internal/worldmodel"
)

// findingAttrs 是 finding 晋升成世界模型 KindFinding 节点时写入 Attrs 的漏洞元数据
type findingAttrs struct {
	FindingID string `json:"finding_id"`
	Seq       int64  `json:"seq,omitempty"`
	Severity  string `json:"severity,omitempty"`
	Summary   string `json:"summary"`
	CWEID     string `json:"cwe_id,omitempty"`
	Category  string `json:"category,omitempty"`
}

// AttemptFromFinding 将二进制漏洞 finding 转为 Attempt（待验证节点）
func AttemptFromFinding(taskID string, f finding.VulnFinding) (verifier.Attempt, bool, error) {
	// 提取复现配方（Primitives）
	// 无复现配方即不可晋升：Verifier 无从复现，跳过
	if len(f.Repro) == 0 || string(f.Repro) == "{}" {
		return verifier.Attempt{}, false, nil
	}

	// 构造节点属性
	attrs := findingAttrs{
		FindingID: f.ID,
		Seq:       f.Seq,
		Severity:  f.Severity,
		Summary:   f.Summary,
		CWEID:     f.CWEID,
		Category:  extractCategory(f.Summary),
	}
	attrsJSON, _ := json.Marshal(attrs)

	// 构造目标 ref
	target := binaryTargetRef(f)

	return verifier.Attempt{
		TaskID:     taskID,
		Kind:       worldmodel.KindFinding,
		Target:     target,
		Primitives: f.Repro,
		Attrs:      attrsJSON,
	}, true, nil
}

// binaryTargetRef 从 finding 派生 binary 目标的 ref
func binaryTargetRef(f finding.VulnFinding) worldmodel.TargetRef {
	locator := f.Host // 二进制文件路径或进程名

	// 尝试从 Target 提取更精确的定位信息
	if len(f.Target) > 0 {
		var t struct {
			Path   string `json:"path"`
			Offset string `json:"offset"`
			Symbol string `json:"symbol"`
		}
		if err := json.Unmarshal(f.Target, &t); err == nil {
			if t.Path != "" {
				locator = t.Path
			}
			if t.Symbol != "" {
				locator = locator + "::" + t.Symbol
			} else if t.Offset != "" {
				locator = locator + "+0x" + t.Offset
			}
		}
	}

	return worldmodel.TargetRef{
		Domain:  "binary",
		RefKind: mapCategoryToRefKind(extractCategory(f.Summary)),
		Locator: locator,
	}
}

// extractCategory 从 summary 提取漏洞类型
func extractCategory(summary string) string {
	lower := strings.ToLower(summary)

	// 常见二进制漏洞关键词
	keywords := map[string]string{
		"buffer overflow": "buffer_overflow",
		"stack overflow":  "stack_overflow",
		"heap overflow":   "heap_overflow",
		"format string":   "format_string",
		"use after free":  "use_after_free",
		"double free":     "double_free",
		"integer overflow": "integer_overflow",
		"null deref":      "null_deref",
		"race condition":  "race_condition",
		"no pie":          "no_pie",
		"no canary":       "no_canary",
		"no relro":        "no_relro",
		"flag":            "flag",
		"gadget":          "gadget",
		"rop":             "rop_chain",
	}

	for kw, cat := range keywords {
		if strings.Contains(lower, kw) {
			return cat
		}
	}

	return "vulnerability"
}

// mapCategoryToRefKind 将漏洞类型映射为世界模型的 ref_kind
func mapCategoryToRefKind(category string) string {
	switch category {
	case "buffer_overflow", "format_string", "use_after_free", "double_free",
		"integer_overflow", "stack_overflow", "heap_overflow", "null_deref",
		"race_condition", "logic_error":
		return "vulnerability"

	case "no_pie", "no_canary", "no_relro", "executable_stack",
		"weak_crypto", "hardcoded_secret":
		return "weakness"

	case "flag", "key", "credential", "sensitive_data":
		return "artifact"

	case "gadget", "rop_chain", "shellcode", "syscall":
		return "capability"

	default:
		return "vulnerability"
	}
}

