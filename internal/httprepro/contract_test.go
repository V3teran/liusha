package httprepro

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	evaluatorcontract "github.com/V3teran/liusha/internal/evaluator/contract"
	executorcontract "github.com/V3teran/liusha/internal/executor/contract"
)

// 执行者契约逐一讲到 http 配方断言的每个谓词与最小子串长度：契约是 LLM 写配方的唯一依据，
// 与归一化、复现通道的形状不得漂移。
func TestExecutorContractDescribesHTTPRecipe(t *testing.T) {
	typ := reflect.TypeOf(Assertion{})
	for i := range typ.NumField() {
		name, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
		if !strings.Contains(executorcontract.Text, "`"+name+"`") {
			t.Errorf("执行者契约缺少断言谓词 %s", name)
		}
	}
	if want := fmt.Sprintf("至少 %d 个字符", MinBodyContainsLen); !strings.Contains(executorcontract.Text, want) {
		t.Errorf("执行者契约应写明 body_contains 最小长度：%s", want)
	}
	if !strings.Contains(executorcontract.Text, `"kind": "`+Name+`"`) {
		t.Errorf("执行者契约应以 kind=%s 说明 http 配方", Name)
	}
}

// 评估者契约逐一讲到 http 复现证据的每个判读字段（method/url 是请求本身，不必解释）：
// 证据怎么读只有契约一处说明，与复现通道的证据形状不得漂移。
func TestEvaluatorContractDescribesHTTPEvidence(t *testing.T) {
	typ := reflect.TypeOf(replayEvidence{})
	for i := range typ.NumField() {
		name, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
		if name == "method" || name == "url" {
			continue
		}
		if !strings.Contains(evaluatorcontract.Text, "`"+name+"`") {
			t.Errorf("评估者契约缺少证据字段 %s", name)
		}
	}
}
