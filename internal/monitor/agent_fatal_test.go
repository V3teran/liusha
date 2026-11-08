package monitor

import (
	"errors"
	"fmt"
	"testing"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/framework/llm"
)

// LLM 配置错误终止 monitor 循环；其余评估失败记录后继续（监察失败不应拖垮任务）。
func TestEvaluationFailure_ConfigErrorIsFatal(t *testing.T) {
	a := &Agent{logger: zerolog.Nop()}
	cfgErr := fmt.Errorf("ReAct runtime execution: %w", &llm.HTTPError{Code: 403})
	if err := a.evaluationFailure(cfgErr, "周期评估"); !errors.Is(err, cfgErr) {
		t.Fatalf("配置错误应返回，得 %v", err)
	}
	if err := a.evaluationFailure(errors.New("parse verdict"), "周期评估"); err != nil {
		t.Fatalf("普通评估失败不应终止循环，得 %v", err)
	}
}
