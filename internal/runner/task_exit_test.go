package runner

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/V3teran/liusha/internal/cognition"
)

// TestClassifyRunExit 锁定 run 结束后的收尾分流。重点是进程停机：asynq 停机超时后把
// 消息推回队列并取消 handler ctx——这是中断而非终止，任务必须保持进行中由恢复监控续跑。
// 此前它与外部终止同口径落 aborted：硬杀的任务能自动续跑，优雅停机（发版的常态）反而
// 把所有运行中任务永久终结。
func TestClassifyRunExit(t *testing.T) {
	cases := []struct {
		name       string
		ctxErr     error
		err        error
		stopReason string
		want       runExit
	}{
		{"自然收敛", nil, nil, "objective_completed", runExitCompleted},
		{"用户停止（任务行已终态）", nil, nil, cognition.StopReasonTerminated, runExitTerminated},
		{"run 超时（deadline）", context.DeadlineExceeded, nil, "context_cancelled", runExitTerminated},
		{"暂停", nil, nil, cognition.StopReasonPaused, runExitPaused},
		{"执行出错", nil, errors.New("agent 故障"), "", runExitFailed},
		{"进程停机：handler ctx 取消", context.Canceled, nil, "context_cancelled", runExitInterrupted},
		{"进程停机且循环带出取消错误", context.Canceled, fmt.Errorf("cognition: %w", context.Canceled), "", runExitInterrupted},
		{"停机与暂停同时发生：按暂停（任务同样不动）", context.Canceled, nil, cognition.StopReasonPaused, runExitPaused},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyRunExit(tc.ctxErr, tc.err, tc.stopReason); got != tc.want {
				t.Fatalf("classifyRunExit(%v, %v, %q) = %v, want %v", tc.ctxErr, tc.err, tc.stopReason, got, tc.want)
			}
		})
	}
}
