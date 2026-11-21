package dispatch

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/V3teran/liusha/internal/task"
)

// 所有下发入口都在创建任何东西之前校验目标长度：Service 不带任何存储依赖，
// 校验若晚于建会话 / 批次 / 模板，这里会因空依赖崩溃而不是返回 ErrGoalTooLong。
func TestDispatchEntriesRejectOverlongGoalBeforeCreatingAnything(t *testing.T) {
	s := &Service{}
	long := strings.Repeat("x", task.MaxGoalRunes+1)
	ctx := context.Background()

	if _, _, err := s.DispatchBatch(ctx, "t", "", []string{"正常目标", long}, nil); !errors.Is(err, task.ErrGoalTooLong) {
		t.Errorf("批量下发：应返回 ErrGoalTooLong，得 %v", err)
	}
	if _, _, err := s.DispatchScheduled(ctx, "t", "", []string{long}, "0 3 * * *", nil); !errors.Is(err, task.ErrGoalTooLong) {
		t.Errorf("定时模板：应返回 ErrGoalTooLong，得 %v", err)
	}
	if _, _, err := s.StartChatTask(ctx, long, nil, nil); !errors.Is(err, task.ErrGoalTooLong) {
		t.Errorf("对话发起：应返回 ErrGoalTooLong，得 %v", err)
	}
}
