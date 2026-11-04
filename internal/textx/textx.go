// Package textx 提供跨包的文本整形工具（rune 安全截断等）。
package textx

// TruncateRunes 按 rune 截尾：s 的 rune 数超过 maxRunes 时截断并追加 suffix。
// maxRunes <= 0 或未超限时原样返回。suffix 由调用方按语境给定
// （如 "…（截断）"、"…（后文截断）"）。
func TruncateRunes(s string, maxRunes int, suffix string) string {
	if maxRunes <= 0 || len(s) <= maxRunes {
		return s
	}
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + suffix
}
