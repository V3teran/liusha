package vulnfinding

import (
	"encoding/json"
	"fmt"
	"strings"
)

// BACEvidence 是 kind="bac.*" 漏洞 evidence jsonb 的标准 schema。
//
// 字段约定来源：skills/vuln-web-bac/SKILL.md "示例" 与 "Step 7" 定义的 evidence 结构。
// 通过 ValidateEvidence 在 WriteFinding 工具层强制——LLM 写错字段名时直接拒绝，
// 避免 evidence jsonb 因 LLM 心情不同变成结构散乱的 string-bag。
type BACEvidence struct {
	ViolatingIdentities []string            `json:"violating_identities"`
	Responses           []BACResponseSample `json:"responses"`
	Reasoning           string              `json:"reasoning,omitempty"`
}

// BACResponseSample 是 BAC evidence.responses[] 单元素：identity 是凭证身份名，
// status_code 是该身份请求的响应状态码（用于区分 401/403 拒绝 vs 200 允许访问）。
type BACResponseSample struct {
	Identity   string `json:"identity"`
	StatusCode int    `json:"status_code"`
}

// ValidateEvidence 按 kind 强制校验 evidence jsonb 结构。
// 返回非 nil 错误时调用方应拒绝写入 finding。
//
// 当前仅约束 kind 前缀 "bac."；其他漏洞类型通过（不约束）。
// 未来加 SSRF / IDOR / SQLi 时在 switch 加分支扩展即可。
//
// 空 evidence 始终通过——append-only 模式下信息最少的 finding 也合法
// （重发现归并键 dedup_key 已携带主要识别信息）。
func ValidateEvidence(kind string, evidence json.RawMessage) error {
	if len(evidence) == 0 {
		return nil
	}
	if strings.HasPrefix(kind, "bac.") {
		return validateBACEvidence(evidence)
	}
	return nil
}

// validateBACEvidence 解析 BAC evidence 并验证必填字段。
//
// 失败场景：
//   - JSON 不能解码到 BACEvidence
//   - violating_identities 为空（BAC 漏洞必须给出"不该访问的身份"）
//   - responses 为空（必须至少 1 条响应样本佐证）
//   - 任一 response.identity 为空（无法回查具体凭证）
func validateBACEvidence(raw json.RawMessage) error {
	var e BACEvidence
	if err := json.Unmarshal(raw, &e); err != nil {
		return fmt.Errorf("BAC evidence 不符合 schema: %w", err)
	}
	if len(e.ViolatingIdentities) == 0 {
		return fmt.Errorf("BAC evidence.violating_identities 不能为空")
	}
	if len(e.Responses) == 0 {
		return fmt.Errorf("BAC evidence.responses 不能为空")
	}
	for i, r := range e.Responses {
		if r.Identity == "" {
			return fmt.Errorf("BAC evidence.responses[%d].identity 不能为空", i)
		}
	}
	return nil
}
