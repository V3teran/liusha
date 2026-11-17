package evaluator

import (
	"encoding/json"
	"strings"
)

// ClaimFingerprint 是坐实主张的指纹：什么类别的主张、作用在哪个对象的哪一点。评估者
// 坐实时按判定契约给出（contract.md 的 finding 字段），晋升门据此把同一主张的再次确认
// 折叠进既有结果节点。类别词表属于业务领域，由评估者章程的「域指引」给出。
type ClaimFingerprint struct {
	Class  string `json:"class"`  // 主张类别
	Target string `json:"target"` // 主张作用的对象
	Point  string `json:"point"`  // 对象上主张生效的具体位置；没有则空
}

// canonicalFingerprint 把评估者给出的原始指纹归一成规范键。归一方向从宽——大小写折叠、
// 去首尾空白、对象去尾斜杠：去重的目标是合并同义报告，不是区分书写差异。类别或对象缺失、
// 形状损坏时返回 ok=false：该主张不参与语义去重（不丢结果，只是不折叠）。
func canonicalFingerprint(raw json.RawMessage) (string, bool) {
	var fp ClaimFingerprint
	if len(raw) == 0 || json.Unmarshal(raw, &fp) != nil {
		return "", false
	}
	norm := func(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
	class, target := norm(fp.Class), strings.TrimRight(norm(fp.Target), "/")
	if class == "" || target == "" {
		return "", false
	}
	return class + "|" + target + "|" + norm(fp.Point), true
}
