package traffic

import (
	"encoding/json"
	"strings"
)

// defaultMaxBody 是 body 截断阈值（32 KiB）；两个 store 共用。
const defaultMaxBody = 32 * 1024

// truncate 把超出 max 的 byte 切片截到 max；max<=0 表示禁用截断。
// 返回前 max 字节的副本，避免持有 caller 大切片的底层数组。
func truncate(b []byte, max int) []byte {
	if max <= 0 || len(b) <= max {
		return b
	}
	out := make([]byte, max)
	copy(out, b[:max])
	return out
}

// normalizeHeaders 把空 RawMessage 折成 jsonb 空对象，避免列默认值与显式 NULL 的歧义。
func normalizeHeaders(h json.RawMessage) []byte {
	if len(h) == 0 {
		return []byte("{}")
	}
	return []byte(h)
}

// nullIfEmpty 把空字符串转 nil（pgx 写 NULL），非空原样返回——用于 nullable text 列。
func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// sqlEscapeLike 转义 LIKE 模式里的 % 和 _ —— 但保留 *（上层转换为 %）。
func sqlEscapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

// extractHost 从 URL 提取 host（不含 port / path / query）。scheme 可选，遇 :/?# 中止。
func extractHost(rawURL string) string {
	s := stripScheme(rawURL)
	for i, r := range s {
		if r == '/' || r == ':' || r == '?' || r == '#' {
			return s[:i]
		}
	}
	return s
}

// extractPath 从 URL 抽 path（不含 query）。空或异常 fallback "/"。
func extractPath(rawURL string) string {
	if rawURL == "" {
		return "/"
	}
	s := stripScheme(rawURL)
	slash := strings.IndexByte(s, '/')
	if slash < 0 {
		return "/"
	}
	s = s[slash:]
	for i, r := range s {
		if r == '?' || r == '#' {
			return s[:i]
		}
	}
	return s
}

// stripScheme 去掉 http(s):// 前缀。
func stripScheme(s string) string {
	for _, prefix := range []string{"https://", "http://"} {
		if len(s) > len(prefix) && s[:len(prefix)] == prefix {
			return s[len(prefix):]
		}
	}
	return s
}
