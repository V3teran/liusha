package cognition

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"

	"github.com/V3teran/liusha/internal/agentrun"
	"github.com/V3teran/liusha/internal/task"
)

// fakeTaskReader 按序返回任务行（最后一个重复返回）。
type fakeTaskReader struct {
	rows []task.Task
	err  error
}

func (f *fakeTaskReader) GetByID(context.Context, string) (task.Task, error) {
	if f.err != nil {
		return task.Task{}, f.err
	}
	row := f.rows[0]
	if len(f.rows) > 1 {
		f.rows = f.rows[1:]
	}
	return row, nil
}

// fakeRunReader 按序返回本 run 的行（最后一个重复返回）；监视循环在别的协程里读，可并发改写。
type fakeRunReader struct {
	mu   sync.Mutex
	rows []agentrun.Run
	err  error
}

func (f *fakeRunReader) GetByID(context.Context, string) (agentrun.Run, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return agentrun.Run{}, f.err
	}
	row := f.rows[0]
	if len(f.rows) > 1 {
		f.rows = f.rows[1:]
	}
	return row, nil
}

// flip 让之后读到的 run 行都是 status。
func (f *fakeRunReader) flip(status agentrun.Status) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows = []agentrun.Run{{ID: "r1", Status: status}}
}

func runningRun() *fakeRunReader {
	return &fakeRunReader{rows: []agentrun.Run{{ID: "r1", Status: agentrun.StatusRunning}}}
}

func TestTaskWatcher_ObservesLifecycle(t *testing.T) {
	cases := []struct {
		name     string
		row      task.Task
		err      error
		stopWhy  string
		finished bool
	}{
		{"进行中", task.Task{Status: task.StatusActive}, nil, "", false},
		{"已停止", task.Task{Status: task.StatusAborted}, nil, StopReasonTerminated, true},
		{"已完成", task.Task{Status: task.StatusCompleted}, nil, StopReasonTerminated, true},
		{"暂停", task.Task{Status: task.StatusActive, Paused: true}, nil, StopReasonPaused, true},
		{"读取失败（下轮重试）", task.Task{}, errors.New("db down"), "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stopped string
			w := &taskWatcher{
				taskID:  "t1",
				tasks:   &fakeTaskReader{rows: []task.Task{tc.row}, err: tc.err},
				runs:    runningRun(),
				stopRun: func(reason string) { stopped = reason },
				logger:  zerolog.Nop(),
			}
			assert.Equal(t, tc.finished, w.observe(context.Background()))
			assert.Equal(t, tc.stopWhy, stopped)
		})
	}
}

// 目标被调整（任务行 goal 变化）时通知一次：规划者立即按新目标重规划、监察者立即复核
// 在途动作；目标未变不通知。
func TestTaskWatcher_NotifiesGoalChange(t *testing.T) {
	changes := 0
	w := &taskWatcher{
		taskID: "t1",
		tasks: &fakeTaskReader{rows: []task.Task{
			{Status: task.StatusActive, Goal: "原目标"},
			{Status: task.StatusActive, Goal: "调整后的目标"},
			{Status: task.StatusActive, Goal: "调整后的目标"},
		}},
		goal:          "原目标",
		runs:          runningRun(),
		stopRun:       func(string) { t.Fatal("目标调整不应结束 run") },
		onGoalChanged: func() { changes++ },
		logger:        zerolog.Nop(),
	}
	for range 3 {
		w.observe(context.Background())
	}
	assert.Equal(t, 1, changes)
}

// run 行已不是 running：本 run 已被回收——恢复监控判它停摆、重投了后继 run，或任务暂停、终态时
// 只回收了 run 行。停 agents、以「被回收」结束本 run，收尾不碰后继 run 接手的任务。这个判断先于
// 任务行：回收已经落定，再按任务状态写任何一行都是多余的。读失败记录后等下一轮。
func TestTaskWatcher_StopsWhenRunIsReclaimed(t *testing.T) {
	for _, status := range []agentrun.Status{agentrun.StatusError, agentrun.StatusAborted, agentrun.StatusCompleted} {
		for _, tk := range []task.Task{
			{Status: task.StatusActive},               // 任务归后继 run
			{Status: task.StatusActive, Paused: true}, // 暂停中的任务只回收了 run 行
			{Status: task.StatusAborted},              // 中断达上限：run 与任务都被恢复监控落定
		} {
			var stopped string
			w := &taskWatcher{
				taskID:  "t1",
				tasks:   &fakeTaskReader{rows: []task.Task{tk}},
				runs:    &fakeRunReader{rows: []agentrun.Run{{ID: "r1", Status: status}}},
				stopRun: func(reason string) { stopped = reason },
				logger:  zerolog.Nop(),
			}
			assert.True(t, w.observe(context.Background()), "run=%s task=%+v", status, tk.Status)
			assert.Equal(t, StopReasonReclaimed, stopped, "run=%s task=%+v", status, tk.Status)
		}
	}

	// 任务终态、run 行却仍在运行：照旧按任务终态收尾
	var stopped string
	w := &taskWatcher{
		tasks:   &fakeTaskReader{rows: []task.Task{{Status: task.StatusAborted}}},
		runs:    runningRun(),
		stopRun: func(reason string) { stopped = reason },
		logger:  zerolog.Nop(),
	}
	assert.True(t, w.observe(context.Background()))
	assert.Equal(t, StopReasonTerminated, stopped)

	// run 行读不到：不下结论，等下一轮
	stopped = ""
	w = &taskWatcher{
		tasks:   &fakeTaskReader{rows: []task.Task{{Status: task.StatusActive}}},
		runs:    &fakeRunReader{err: errors.New("db down")},
		stopRun: func(reason string) { stopped = reason },
		logger:  zerolog.Nop(),
	}
	assert.False(t, w.observe(context.Background()))
	assert.Empty(t, stopped)
}

// run 行被回收后的下一个轮询周期内停 agents：曾有一轮 run 被判停摆并重投之后继续在进程里跑了
// 四十分钟，与四轮后继 run 同时在同一个任务上规划。
func TestTaskWatcher_StopsPromptlyAfterReclaim(t *testing.T) {
	stopped := make(chan string, 1)
	runs := &fakeRunReader{rows: []agentrun.Run{{ID: "r1", Status: agentrun.StatusRunning}}}
	w := &taskWatcher{
		taskID:   "t1",
		tasks:    &fakeTaskReader{rows: []task.Task{{Status: task.StatusActive}}},
		runs:     runs,
		interval: 5 * time.Millisecond,
		stopRun:  func(reason string) { stopped <- reason },
		logger:   zerolog.Nop(),
	}
	stop := w.start(context.Background())
	defer stop()
	// 先确认监视在跑且不误停，再让 run 行被回收
	select {
	case reason := <-stopped:
		t.Fatalf("run 仍在运行就被结束：%s", reason)
	case <-time.After(40 * time.Millisecond):
	}
	runs.flip(agentrun.StatusError)
	select {
	case reason := <-stopped:
		assert.Equal(t, StopReasonReclaimed, reason)
	case <-time.After(2 * time.Second):
		t.Fatal("run 行被回收后应在一个轮询周期内停 agents")
	}
}
