package lateral

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

// AttemptFromFinding 将内网横向移动 finding 转为 Attempt（待验证节点）
func AttemptFromFinding(taskID string, f finding.VulnFinding) (verifier.Attempt, bool, error) {
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
	target := lateralTargetRef(f)

	return verifier.Attempt{
		TaskID:     taskID,
		Kind:       worldmodel.KindFinding,
		Target:     target,
		Primitives: f.Repro,
		Attrs:      attrsJSON,
	}, true, nil
}

// lateralTargetRef 从 finding 派生 lateral 目标的 ref
func lateralTargetRef(f finding.VulnFinding) worldmodel.TargetRef {
	locator := f.Host // 内网目标（主机名/IP）

	// 尝试从 Target 提取更精确的定位信息
	if len(f.Target) > 0 {
		var t struct {
			Hostname string `json:"hostname"`
			IP       string `json:"ip"`
			Domain   string `json:"domain"`
			User     string `json:"user"`
		}
		if err := json.Unmarshal(f.Target, &t); err == nil {
			if t.Hostname != "" {
				locator = t.Hostname
			} else if t.IP != "" {
				locator = t.IP
			}
			if t.Domain != "" {
				locator = t.Domain + "\\" + locator
			}
			if t.User != "" {
				locator = locator + "::" + t.User
			}
		}
	}

	return worldmodel.TargetRef{
		Domain:  "lateral",
		RefKind: mapCategoryToRefKind(extractCategory(f.Summary)),
		Locator: locator,
	}
}

// extractCategory 从 summary 提取内网渗透类型
func extractCategory(summary string) string {
	lower := strings.ToLower(summary)

	// 常见内网渗透关键词
	keywords := map[string]string{
		"plaintext password": "plaintext_password",
		"ntlm hash":          "ntlm_hash",
		"kerberos":           "kerberos_ticket",
		"smb":                "smb_access",
		"rdp":                "rdp_access",
		"ssh":                "ssh_access",
		"domain admin":       "domain_admin",
		"local admin":        "local_admin",
		"scheduled task":     "scheduled_task",
		"ms17-010":           "ms17_010",
		"zerologon":          "zerologon",
		"printnightmare":     "printnightmare",
	}

	for kw, cat := range keywords {
		if strings.Contains(lower, kw) {
			return cat
		}
	}

	return "access"
}

// mapCategoryToRefKind 将内网渗透类型映射为世界模型的 ref_kind
func mapCategoryToRefKind(category string) string {
	switch category {
	case "plaintext_password", "ntlm_hash", "kerberos_ticket",
		"lsass_dump", "cached_credential", "dpapi_secret":
		return "credential"

	case "smb_access", "rdp_access", "ssh_access", "winrm_access",
		"psexec", "wmi_access", "dcom_access":
		return "access"

	case "domain_admin", "local_admin", "service_account",
		"gpo_abuse", "acl_abuse", "delegation_abuse":
		return "privilege"

	case "scheduled_task", "service_creation", "registry_run_key",
		"startup_folder", "wmi_subscription", "golden_ticket":
		return "persistence"

	case "workstation", "server", "domain_controller", "file_server",
		"database_server", "web_server":
		return "host"

	case "ms17_010", "zerologon", "printnightmare", "petitpotam",
		"samaccountname_spoofing", "nopac":
		return "vulnerability"

	default:
		return "access"
	}
}

