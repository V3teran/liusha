package vulnfinding

import (
	"net"
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

// NormalizeDedupKey 把 dedup_key 模板化：
//   - host 段去端口（49.234.23.42:8888 → 49.234.23.42），避免同 host 因含端口/不含端口被视作两条
//   - path 段动态值占位（纯数字/UUID/长 hex → :id / :uuid / :hex）
//
// 算法：dedup_key 按 ':' 拆段，约定 <kind>:<host>:<method>:<path>。
// 但 host 自身可能含 ':<port>'（如 IP:port），LLM 输出时也可能含端口；
// 因此先用启发式从尾部找 method 边界（HTTP method 是大写英文），再回切出 host。
//
// 示例：
//
//	bac.horizontal_priv_esc:vulnapp:GET:/api/order/12345
//	→ bac.horizontal_priv_esc:vulnapp:GET:/api/order/:id
//
//	sqli.error_based:49.234.23.42:8888:GET:/vulnerabilities/sqli/:id
//	→ sqli.error_based:49.234.23.42:GET:/vulnerabilities/sqli/:id   （host 去端口 + path 不变）
//
//	bac.horizontal_priv_esc:vulnapp:GET:/api/file/550e8400-e29b-41d4-a716-446655440000
//	→ bac.horizontal_priv_esc:vulnapp:GET:/api/file/:uuid
//
// 入参为空字符串时原样返回（让上层 Store.Save 报"dedup_key 必填"错误）。
func NormalizeDedupKey(rawKey string) string {
	if rawKey == "" {
		return rawKey
	}
	kind, host, method, path, ok := splitDedupKey(rawKey)
	if !ok {
		// 不符合预期格式 → 不动它，让上层自行处理。
		return rawKey
	}
	host = stripHostPort(host)
	path = TemplatizePath(path)
	return kind + ":" + host + ":" + method + ":" + path
}

// httpMethodPattern 匹配段是否为 HTTP method（用于分辨 host 段中可能含的 ':<port>'）。
var httpMethodPattern = regexp.MustCompile(`^[A-Z]{3,7}$`)

// splitDedupKey 把 dedup_key 拆成 (kind, host, method, path)。
//
// 处理 host 含 ':<port>' 的歧义：先用 SplitN(., 4) 拿前 3 段；若 parts[2]（推定 method 位置）
// 不是 HTTP 方法格式，说明 host 含端口被错切——往后多吞一段，把端口拼回 host。
func splitDedupKey(rawKey string) (kind, host, method, path string, ok bool) {
	parts := strings.Split(rawKey, ":")
	if len(parts) < 4 {
		return "", "", "", "", false
	}
	// 标准情况：<kind>:<host>:<method>:<path...>
	if httpMethodPattern.MatchString(parts[2]) {
		return parts[0], parts[1], parts[2], strings.Join(parts[3:], ":"), true
	}
	// host 含 ':<port>' → parts[2] 是端口数字，parts[3] 才是 method。
	if len(parts) >= 5 && httpMethodPattern.MatchString(parts[3]) {
		return parts[0], parts[1] + ":" + parts[2], parts[3], strings.Join(parts[4:], ":"), true
	}
	return "", "", "", "", false
}

// stripHostPort 去掉 host 末尾的 :port；纯主机名/IP 不变。
// 与 e2e-bac 同名 helper 同语义，独立实现避免跨包依赖。
func stripHostPort(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return host
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
