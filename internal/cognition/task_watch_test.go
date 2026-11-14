package cognition

import (
	"context"
	"errors"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"

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
				stopRun: func(reason string) { stopped = reason },
				logger:  zerolog.Nop(),
			}
			assert.Equal(t, tc.finished, w.observe(context.Background()))
			assert.Equal(t, tc.stopWhy, stopped)
		})
	}
}
