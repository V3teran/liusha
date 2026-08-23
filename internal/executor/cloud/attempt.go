package cloud

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

// AttemptFromFinding 将云环境漏洞 finding 转为 Attempt（待验证节点）
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
	target := cloudTargetRef(f)

	return verifier.Attempt{
		TaskID:     taskID,
		Kind:       worldmodel.KindFinding,
		Target:     target,
		Primitives: f.Repro,
		Attrs:      attrsJSON,
	}, true, nil
}

// cloudTargetRef 从 finding 派生 cloud 目标的 ref
func cloudTargetRef(f finding.VulnFinding) worldmodel.TargetRef {
	locator := f.Host // 云资源标识（账户ID/订阅ID/项目ID）

	// 尝试从 Target 提取更精确的资源标识
	if len(f.Target) > 0 {
		var t struct {
			ARN          string `json:"arn"`           // AWS
			ResourceID   string `json:"resource_id"`   // Azure
			ResourceName string `json:"resource_name"` // GCP
		}
		if err := json.Unmarshal(f.Target, &t); err == nil {
			if t.ARN != "" {
				locator = t.ARN
			} else if t.ResourceID != "" {
				locator = t.ResourceID
			} else if t.ResourceName != "" {
				locator = t.ResourceName
			}
		}
	}

	return worldmodel.TargetRef{
		Domain:  "cloud",
		RefKind: mapCategoryToRefKind(extractCategory(f.Summary)),
		Locator: locator,
	}
}

// extractCategory 从 summary 提取配置错误类型
func extractCategory(summary string) string {
	lower := strings.ToLower(summary)

	// 常见云安全问题关键词
	keywords := map[string]string{
		"public bucket":          "public_bucket",
		"open security group":    "open_security_group",
		"overprivileged":         "overprivileged_iam",
		"no mfa":                 "no_mfa",
		"leaked access key":      "leaked_access_key",
		"exposed service account": "exposed_service_account",
		"privilege escalation":   "privilege_escalation",
		"assume role":            "assume_role",
		"ec2":                    "ec2_instance",
		"s3":                     "s3_bucket",
		"lambda":                 "lambda_function",
		"rds":                    "rds_instance",
	}

	for kw, cat := range keywords {
		if strings.Contains(lower, kw) {
			return cat
		}
	}

	return "misconfiguration"
}

// mapCategoryToRefKind 将配置错误类型映射为世界模型的 ref_kind
func mapCategoryToRefKind(category string) string {
	switch category {
	case "public_bucket", "open_security_group", "overprivileged_iam",
		"no_mfa", "weak_password_policy", "insecure_encryption",
		"exposed_database", "public_snapshot":
		return "misconfiguration"

	case "leaked_access_key", "exposed_service_account", "hardcoded_credential",
		"leaked_api_key", "exposed_secret":
		return "credential"

	case "privilege_escalation", "assume_role", "cross_account_access",
		"container_escape", "lambda_layer_injection":
		return "privilege"

	case "ec2_instance", "s3_bucket", "rds_instance", "lambda_function",
		"api_gateway", "ecs_task", "eks_cluster":
		return "resource"

	case "iam_service", "sts_service", "kms_service", "cloudtrail_service",
		"config_service":
		return "service"

	default:
		return "misconfiguration"
	}
}

