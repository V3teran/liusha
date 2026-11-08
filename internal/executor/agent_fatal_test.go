package executor

import (
	"errors"
	"fmt"
	"testing"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/framework/llm"
)

// LLM 配置错误（鉴权/权限/模型不存在）终止 executor 循环；其余执行失败记录后继续。
func TestLoopFailure_ConfigErrorIsFatal(t *testing.T) {
	a := &Agent{logger: zerolog.Nop()}
	cfgErr := fmt.Errorf("execute action a1: LLM 调用失败: %w", &llm.HTTPError{Code: 401})
	if err := a.loopFailure(cfgErr, "定期检查"); !errors.Is(err, cfgErr) {
		t.Fatalf("配置错误应返回，得 %v", err)
	}
	if err := a.loopFailure(errors.New("sandbox exec: exit 1"), "定期检查"); err != nil {
		t.Fatalf("普通执行失败不应终止循环，得 %v", err)
	}
	if err := a.loopFailure(nil, "定期检查"); err != nil {
		t.Fatalf("nil 应返回 nil，得 %v", err)
	}
}
