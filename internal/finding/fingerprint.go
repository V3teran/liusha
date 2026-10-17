package finding

import (
	"encoding/json"
	"strings"
)

// ClaimFingerprint 是本域主张指纹的形状：裁决官按章程契约输出，描述
// "什么类别的主张、打在哪个位置"。同一位置的同类主张共享同一规范键，
// 晋升门据此折叠重复确认。
type ClaimFingerprint struct {
	Class  string `json:"class"`  // 主张类别，小写 snake_case（章程载明词表）
	Method string `json:"method"` // 请求方法
	Host   string `json:"host"`   // 目标主机（含端口）
	Path   string `json:"path"`   // URL 路径（不含 query）
	Param  string `json:"param"`  // 触发点（参数名/头名/正文字段），无则空
}

// canonicalKey 归一方向从宽：大小写折叠、去首尾空白、路径去尾斜杠——
// 去重的目标是合并同义报告，不是区分书写差异。
func (f *ClaimFingerprint) canonicalKey() string {
	norm := func(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
	return strings.Join([]string{
		norm(f.Class),
		norm(f.Method),
		norm(f.Host),
		strings.TrimSuffix(norm(f.Path), "/"),
		norm(f.Param),
	}, "|")
}

// FingerprintNormalizer 实现 evaluator.FingerprintNormalizer：把裁决官
// 输出的原始指纹 JSON 归一成规范键。形状损坏返回 ok=false——该主张不
// 参与语义去重，按无指纹处理（不丢发现，只是不折叠）。
type FingerprintNormalizer struct{}

// CanonicalKey 归一原始指纹。
func (FingerprintNormalizer) CanonicalKey(raw json.RawMessage) (string, bool) {
	var fp ClaimFingerprint
	if json.Unmarshal(raw, &fp) != nil {
		return "", false
	}
	return fp.canonicalKey(), true
}
