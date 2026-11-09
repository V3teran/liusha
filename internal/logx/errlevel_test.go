package logx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

// 任务终止、进程停止时，在途的查询与 LLM 调用随上下文取消而失败——这是正常收尾，
// 不是故障；超时（DeadlineExceeded）则是真实故障，仍按 error 记录。
func TestErrorUnlessCanceled(t *testing.T) {
	cases := []struct {
		name  string
		err   error
		level string
	}{
		{"取消降为 debug", fmt.Errorf("list nodes: %w", context.Canceled), `"level":"debug"`},
		{"超时仍是 error", fmt.Errorf("llm: %w", context.DeadlineExceeded), `"level":"error"`},
		{"其他错误是 error", errors.New("boom"), `"level":"error"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			l := zerolog.New(&buf).Level(zerolog.DebugLevel)
			ErrorUnlessCanceled(l, tc.err).Err(tc.err).Msg("m")
			if !strings.Contains(buf.String(), tc.level) {
				t.Fatalf("期望 %s，得 %s", tc.level, buf.String())
			}
		})
	}
}
