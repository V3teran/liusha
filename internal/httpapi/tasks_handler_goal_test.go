package httpapi

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/task"
)

// overlongDispatch 模拟服务层的目标长度校验失败。
type overlongDispatch struct{}

func (overlongDispatch) DispatchBatch(context.Context, string, string, []string, *string) (string, []DispatchedTask, error) {
	return "", nil, fmt.Errorf("第 2 行: %w", task.ErrGoalTooLong)
}

func (overlongDispatch) DispatchScheduled(context.Context, string, string, []string, string, *string) (string, time.Time, error) {
	return "", time.Time{}, fmt.Errorf("第 1 行: %w", task.ErrGoalTooLong)
}

// 目标超长是调用方可修复的输入错误：批量下发与定时模板都回 400，而不是 500。
func TestCreateTasks_OverlongGoalIs400(t *testing.T) {
	srv := newTestServer(t, Deps{TaskDispatch: overlongDispatch{}})
	defer srv.Close()
	for _, body := range []string{
		`{"title":"t","description":"","briefs":["a","b"],"cron":""}`,
		`{"title":"t","description":"","briefs":["a"],"cron":"0 3 * * *"}`,
	} {
		resp := doReq(t, "POST", srv.URL+"/api/tasks", body)
		_ = resp.Body.Close()
		if resp.StatusCode != 400 {
			t.Errorf("%s 应 400，得 %d", body, resp.StatusCode)
		}
	}
}
