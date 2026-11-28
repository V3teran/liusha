package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/task"
)

const (
	steerTask   = "22222222-2222-4222-8222-222222222222"
	steerAction = "33333333-3333-4333-8333-333333333333"
	missingID   = "00000000-0000-0000-0000-000000000000"
)

// fakeSteering 是 TaskSteeringAPI 的替身：missingID 按真实实现包装 pgx.ErrNoRows / ErrActionNotFound，
// err 非空时所有操作返回它；记录最后一次调用的参数。
type fakeSteering struct {
	err      error
	goal     string
	assigned struct {
		instruction string
	}
	rejectReason string
}

func (f *fakeSteering) check(taskID string) error {
	if taskID == missingID {
		return fmt.Errorf("锁定任务 %s: %w", taskID, pgx.ErrNoRows)
	}
	return f.err
}

func (f *fakeSteering) AdjustGoal(_ context.Context, taskID, goal string) error {
	if err := f.check(taskID); err != nil {
		return err
	}
	f.goal = goal
	return nil
}

func (f *fakeSteering) AssignAction(_ context.Context, taskID, instruction string) (string, error) {
	if err := f.check(taskID); err != nil {
		return "", err
	}
	f.assigned.instruction = instruction
	return steerAction, nil
}

func (f *fakeSteering) RejectAction(_ context.Context, taskID, actionID, reason string) error {
	if err := f.check(taskID); err != nil {
		return err
	}
	if actionID == missingID {
		return fmt.Errorf("%w: %s", explorationgraph.ErrActionNotFound, actionID)
	}
	f.rejectReason = reason
	return nil
}

// fakeTaskSummaries 返回固定的任务摘要（PATCH 成功后回读）。
type fakeTaskSummaries struct{ fakeTasksList }

func (f *fakeTaskSummaries) GetSummary(_ context.Context, id string) (task.Summary, error) {
	return task.Summary{ID: id, Goal: "回读的目标"}, nil
}

func TestPatchTaskGoal(t *testing.T) {
	cases := []struct {
		name string
		path string
		body any
		err  error
		want int
	}{
		{"缺 goal", "/api/tasks/" + steerTask, map[string]any{}, nil, http.StatusBadRequest},
		{"空白目标", "/api/tasks/" + steerTask, map[string]any{"goal": "  \n "}, nil, http.StatusBadRequest},
		{"超长", "/api/tasks/" + steerTask, map[string]any{"goal": strings.Repeat("测", task.MaxGoalRunes+1)}, nil, http.StatusBadRequest},
		{"非法 UUID", "/api/tasks/not-a-uuid", map[string]any{"goal": "g"}, nil, http.StatusBadRequest},
		{"任务不存在", "/api/tasks/" + missingID, map[string]any{"goal": "g"}, nil, http.StatusNotFound},
		{"任务已结束", "/api/tasks/" + steerTask, map[string]any{"goal": "g"}, task.ErrNotActive, http.StatusConflict},
		{"成功", "/api/tasks/" + steerTask, map[string]any{"goal": "  新目标  "}, nil, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeSteering{err: tc.err}
			srv := newTestServer(t, Deps{TaskSteering: fake, Tasks: &fakeTaskSummaries{}})
			defer srv.Close()
			code, body := doJSON(t, "PATCH", srv.URL+tc.path, tc.body)
			if code != tc.want {
				t.Fatalf("want %d got %d (%v)", tc.want, code, body)
			}
			if code == http.StatusOK {
				if fake.goal != "新目标" {
					t.Fatalf("目标应去首尾空白后写入，得 %q", fake.goal)
				}
				if body["goal"] != "回读的目标" {
					t.Fatalf("应返回更新后的任务: %v", body)
				}
			}
		})
	}
}

func TestAssignTaskAction(t *testing.T) {
	cases := []struct {
		name string
		body any
		err  error
		want int
	}{
		{"缺指令", map[string]any{}, nil, http.StatusBadRequest},
		{"超长指令", map[string]any{"instruction": strings.Repeat("测", maxInstructionRunes+1)}, nil, http.StatusBadRequest},
		{"任务已结束", map[string]any{"instruction": "x"}, task.ErrNotActive, http.StatusConflict},
		{"成功", map[string]any{"instruction": " 复测登录 "}, nil, http.StatusCreated},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeSteering{err: tc.err}
			srv := newTestServer(t, Deps{TaskSteering: fake})
			defer srv.Close()
			code, body := doJSON(t, "POST", srv.URL+"/api/tasks/"+steerTask+"/actions", tc.body)
			if code != tc.want {
				t.Fatalf("want %d got %d (%v)", tc.want, code, body)
			}
			if code == http.StatusCreated {
				if body["id"] != steerAction || fake.assigned.instruction != "复测登录" {
					t.Fatalf("指派参数或响应不符: %v %+v", body, fake.assigned)
				}
			}
		})
	}
}

func TestRejectTaskAction(t *testing.T) {
	base := "/api/tasks/" + steerTask + "/actions/"
	cases := []struct {
		name string
		path string
		body any
		err  error
		want int
	}{
		{"缺原因", base + steerAction + "/reject", map[string]any{"reason": " "}, nil, http.StatusBadRequest},
		{"非法动作 UUID", base + "bad/reject", map[string]any{"reason": "x"}, nil, http.StatusBadRequest},
		{"动作不存在", base + missingID + "/reject", map[string]any{"reason": "x"}, nil, http.StatusNotFound},
		{"动作已结束", base + steerAction + "/reject", map[string]any{"reason": "x"}, explorationgraph.ErrActionSettled, http.StatusConflict},
		{"任务已结束", base + steerAction + "/reject", map[string]any{"reason": "x"}, task.ErrNotActive, http.StatusConflict},
		{"成功", base + steerAction + "/reject", map[string]any{"reason": " 超出范围 "}, nil, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeSteering{err: tc.err}
			srv := newTestServer(t, Deps{TaskSteering: fake})
			defer srv.Close()
			code, body := doJSON(t, "POST", srv.URL+tc.path, tc.body)
			if code != tc.want {
				t.Fatalf("want %d got %d (%v)", tc.want, code, body)
			}
			if code == http.StatusOK && fake.rejectReason != "超出范围" {
				t.Fatalf("原因应去首尾空白，得 %q", fake.rejectReason)
			}
		})
	}
}
