package db

import "strings"

// EscapeLike 转义 LIKE / ILIKE 模式里的元字符（\ % _），让用户输入按字面匹配。PG 的 LIKE 缺省
// 转义符是反斜杠，不需要另写 ESCAPE 子句。搜索框的输入一律经它（或 ContainsPattern）进模式：
// 未转义时搜「user_id」会把 _ 当作任意单字符，搜「100%」会把 % 当作任意串。
func EscapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

// ContainsPattern 把搜索框输入转成「包含」模式：去首尾空白、转义元字符、两端补 %。
func ContainsPattern(q string) string {
	return "%" + EscapeLike(strings.TrimSpace(q)) + "%"
}
