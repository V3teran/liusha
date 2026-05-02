package finding

import (
	"regexp"
	"strings"
)

// dedup_key 路径模板化：把同一接口不同实例归并到同一 dedup_key，避免 UPSERT 失效
// 导致同 endpoint 反复写入新 finding。
//
// 使用方式：WriteFinding 工具在调 Store.Save 前调用 NormalizeDedupKey(rawKey)，
// 工具层强制重写——LLM 写错了也能兜底（不能完全信任 LLM 拼对 path 模板）。
//
// 适用 dedup_key 格式（约定）：
//
//	<kind>:<host>:<method>:<path>
//	bac.horizontal_priv_esc:vulnapp:GET:/api/order/12345
//	→ bac.horizontal_priv_esc:vulnapp:GET:/api/order/:id

// 路径段匹配正则——按优先级从严到松依次替换：
//   - UUID（强结构化 ID 优先）
//   - 长 hex 串（≥ 16 位，覆盖 SHA1/SHA256/MongoDB ObjectId 等）
//   - 纯数字（最常见的 ID 形式）
var (
	// uuidPattern 匹配标准 UUID（含 hyphenated 与 hex32 两种）。
	uuidPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$|^[0-9a-f]{32}$`)

	// numericPattern 匹配纯数字段（如 /order/7 或 /user/12345）。
	numericPattern = regexp.MustCompile(`^\d+$`)

	// hexLongPattern 匹配长 hex 串（≥ 16 位），覆盖 SHA1/SHA256/MongoDB ObjectId 等。
	hexLongPattern = regexp.MustCompile(`(?i)^[0-9a-f]{16,}$`)
)

// NormalizeDedupKey 把 dedup_key 中的 path 部分模板化：纯数字/UUID/长 hex → :id / :uuid / :hex。
//
// 算法：把 dedup_key 按 ':' 拆段，前 3 段为 <kind>:<host>:<method>，第 4 段及以后视为 path
// （兼容 path 自身可能含 ':' 的情况，用 SplitN(., 4)）。模板化只动 path 部分。
//
// 示例：
//
//	bac.horizontal_priv_esc:vulnapp:GET:/api/order/12345
//	→ bac.horizontal_priv_esc:vulnapp:GET:/api/order/:id
//
//	bac.unauthorized_access:vulnapp:POST:/api/admin/delete
//	→ bac.unauthorized_access:vulnapp:POST:/api/admin/delete  （无变更）
//
//	bac.horizontal_priv_esc:vulnapp:GET:/api/file/550e8400-e29b-41d4-a716-446655440000
//	→ bac.horizontal_priv_esc:vulnapp:GET:/api/file/:uuid
//
// 入参为空字符串时原样返回（让上层 Store.Save 报"dedup_key 必填"错误）。
func NormalizeDedupKey(rawKey string) string {
	if rawKey == "" {
		return rawKey
	}
	// dedup_key 约定为 <kind>:<host>:<method>:<path>；前 3 段无 ':'，第 4 段开始是 path。
	// path 自己可能含 ':'（极少见，如 matrix params），保险用 SplitN(., 4)。
	parts := strings.SplitN(rawKey, ":", 4)
	if len(parts) < 4 {
		// 不符合预期格式（少于 4 段）→ 不动它，让上层自行处理。
		return rawKey
	}
	prefix := strings.Join(parts[:3], ":")
	path := parts[3]
	templated := TemplatizePath(path)
	return prefix + ":" + templated
}

// TemplatizePath 把 URL path 中的动态段（数字 / UUID / 长 hex）替换为占位符。
//
// 规则（按段独立判断，不跨 '/'）：
//   - UUID 形式 → :uuid
//   - 长 hex 串（≥ 16 位）→ :hex
//   - 纯数字（任何长度）→ :id
//   - 其他保留原样
//
// 示例：
//
//	/api/order/7                                     → /api/order/:id
//	/api/file/550e8400-e29b-41d4-a716-446655440000   → /api/file/:uuid
//	/api/sha/abc123def4567890abcd                    → /api/sha/:hex
//	/api/admin/users                                 → /api/admin/users
//
// 注意：保留 query string 原样（dedup_key 一般不带 query；若带，整段不动）。
func TemplatizePath(path string) string {
	// 拆 query
	var query string
	if idx := strings.Index(path, "?"); idx >= 0 {
		query = path[idx:]
		path = path[:idx]
	}

	segments := strings.Split(path, "/")
	for i, seg := range segments {
		if seg == "" {
			continue
		}
		switch {
		case uuidPattern.MatchString(seg):
			segments[i] = ":uuid"
		case hexLongPattern.MatchString(seg):
			segments[i] = ":hex"
		case numericPattern.MatchString(seg):
			segments[i] = ":id"
		}
	}
	return strings.Join(segments, "/") + query
}
