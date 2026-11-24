package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/agentrun"
)

const runTaskID = "b1a2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d"

// fakeLiveRuns 按任务返回存活 run；没有的返回 nil（与 store 同一口径）。
type fakeLiveRuns map[string]*agentrun.Run

func (f fakeLiveRuns) LiveByTask(_ context.Context, taskID string) (*agentrun.Run, error) {
	return f[taskID], nil
}

func newRunServer(runs LiveRunReader) http.Handler {
	return NewServer(Deps{
		Auth:       newFakeAuthStore(),
		TaskReader: fakeTaskReader{runTaskID: {ID: runTaskID}},
		LiveRuns:   runs,
	})
}

// 任务当前存活的一轮 run 与它的存活心跳：这是外界判断「任务是不是卡住了」应当看的信号。
func TestGetTaskRun(t *testing.T) {
	beat := time.Date(2026, 11, 24, 5, 6, 7, 0, time.UTC)
	h := newRunServer(fakeLiveRuns{runTaskID: {ID: "run-1", TaskID: runTaskID, Status: agentrun.StatusRunning,
		CreatedAt: beat.Add(-time.Hour), HeartbeatAt: beat}})

	rec := getAuthed(t, h, "/api/tasks/"+runTaskID+"/run")
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d, 期望 200；响应 %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Run struct {
			ID          string          `json:"id"`
			Status      agentrun.Status `json:"status"`
			HeartbeatAt time.Time       `json:"heartbeat_at"`
		} `json:"run"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Run.ID != "run-1" || got.Run.Status != agentrun.StatusRunning || !got.Run.HeartbeatAt.Equal(beat) {
		t.Fatalf("应带出存活 run 与它的心跳，实际 %+v", got.Run)
	}
}

// 任务没有存活的 run（已结束、暂停、两次重投之间）时 run 为 null，不是 404：任务本身存在。
func TestGetTaskRun_NoneIsNull(t *testing.T) {
	rec := getAuthed(t, newRunServer(fakeLiveRuns{}), "/api/tasks/"+runTaskID+"/run")
	if rec.Code != http.StatusOK || rec.Body.String() != `{"run":null}` {
		t.Fatalf("没有存活 run 时应 200 且 run 为 null，实际 %d %s", rec.Code, rec.Body.String())
	}
}

func TestGetTaskRun_UnknownTaskIs404(t *testing.T) {
	rec := getAuthed(t, newRunServer(fakeLiveRuns{}), "/api/tasks/00000000-0000-4000-8000-000000000000/run")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("任务不存在应 404，实际 %d", rec.Code)
	}
}

// 依赖没有注入时路由不注册（与其它任务名下资源同一惯例）。
func TestGetTaskRun_NotRegisteredWithoutDependency(t *testing.T) {
	h := NewServer(Deps{Auth: newFakeAuthStore(), TaskReader: fakeTaskReader{runTaskID: {ID: runTaskID}}})
	rec := getAuthed(t, h, "/api/tasks/"+runTaskID+"/run")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("未注入时应不注册该路由（404），实际 %d", rec.Code)
	}
}

// 读 run 失败（库故障）是 500，不是「没有存活 run」。
type failingLiveRuns struct{}

func (failingLiveRuns) LiveByTask(context.Context, string) (*agentrun.Run, error) {
	return nil, errors.New("库不可用")
}

func TestGetTaskRun_StoreFailureIs500(t *testing.T) {
	h := newRunServer(failingLiveRuns{})
	rec := getAuthed(t, h, "/api/tasks/"+runTaskID+"/run")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("读 run 失败应 500，实际 %d", rec.Code)
	}
}
