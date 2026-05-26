package endpoint

import "strings"

// TemplatizePath 把 URL path 中的数字段、UUID、长 hex 替换成占位符——
// 避免 /user/1, /user/2 在攻击面图里分裂成两个独立 endpoint 节点。
//
// 替换规则：
//   - 纯数字段（"1", "12345"）→ ":id"
//   - 标准 UUID（8-4-4-4-12 hex with dash）→ ":uuid"
//   - 长 hex 串（≥ 16 字符且全 hex）→ ":hex"
//   - 统一 trim 尾部 "/"（根 "/" 保留）——多数 server 把 /x 与 /x/ 当同一资源
//
// 设计意图：endpoint dedup（INSERT ON CONFLICT (owner_id, host, method, path)）
// 与 graphview.buildEndpointFromFinding 模板化后的 ID 必须一致——共用本函数保证语义对齐，
// 避免 commander write_endpoint("/vulnerabilities/xss_d") 和 striker finding.target.path="/vulnerabilities/xss_d/"
// 在 graph 上误判为两个独立 endpoint。
func TemplatizePath(p string) string {
	parts := strings.Split(p, "/")
	for i, seg := range parts {
		if seg == "" {
			continue
		}
		switch {
		case isAllDigits(seg):
			parts[i] = ":id"
		case isUUID(seg):
			parts[i] = ":uuid"
		case len(seg) >= 16 && isHex(seg):
			parts[i] = ":hex"
		}
	}
	out := strings.Join(parts, "/")
	// 根 "/" 不能去——空字符串不是合法 path
	if len(out) > 1 {
		out = strings.TrimRight(out, "/")
	}
	return out
}

func isAllDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if !isHexRune(r) {
				return false
			}
		}
	}
	return true
}

func isHex(s string) bool {
	for _, r := range s {
		if !isHexRune(r) {
			return false
		}
	}
	return s != ""
}

func isHexRune(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
}
