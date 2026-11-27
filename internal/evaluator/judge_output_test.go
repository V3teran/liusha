package evaluator

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// 判定终稿的验收就是判定解析器本身：说明文字夹着 JSON 也认——模型在 JSON 前后写的说明不是
// 不合格。取不出 JSON、或 verdict 不是 confirmed / refuted 的不认。
func TestVerdictOutputCheck(t *testing.T) {
	for answer, ok := range map[string]bool{
		`{"verdict":"confirmed","confidence":0.9}`: true,
		"复现成功，基线与利用请求差分明显。\n\n```json\n{\"verdict\":\"confirmed\",\"confidence\":0.95}\n```": true,
		`{"verdict":"refuted","confidence":0.6}`: true,
		`{"confidence":0.9}`:                     false,
		`{"verdict":"judge_unavailable"}`:        false,
		"假设坐实。":                                  false,
		"":                                       false,
	} {
		err := verdictOutputCheck(answer)
		if ok {
			assert.NoError(t, err, answer)
		} else {
			assert.Error(t, err, answer)
		}
	}
}
