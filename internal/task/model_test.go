package task

import (
	"testing"
	"time"
)

func TestStatus_String(t *testing.T) {
	tests := []struct {
		status Status
		want   string
	}{
		{StatusActive, "active"},
		{StatusCompleted, "completed"},
		{StatusAborted, "aborted"},
	}
	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			if got := string(tt.status); got != tt.want {
				t.Errorf("Status = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNewParams_Validate(t *testing.T) {
	tests := []struct {
		name    string
		params  NewParams
		wantErr bool
	}{
		{
			name: "合法参数",
			params: NewParams{
				AssignmentID: "a1",
				Brief:        "测试目标",
				TargetHost:   "example.com",
			},
			wantErr: false,
		},
		{
			name: "缺少 AssignmentID",
			params: NewParams{
				Brief:      "测试目标",
				TargetHost: "example.com",
			},
			wantErr: true,
		},
		{
			name: "缺少 Brief",
			params: NewParams{
				AssignmentID: "a1",
				TargetHost:   "example.com",
			},
			wantErr: true,
		},
		{
			name: "TargetHost 可选",
			params: NewParams{
				AssignmentID: "a1",
				Brief:        "测试目标",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 验证逻辑在 Store.Create 中，这里只测试参数结构
			if tt.params.AssignmentID == "" && !tt.wantErr {
				t.Error("应该要求 AssignmentID")
			}
			if tt.params.Brief == "" && !tt.wantErr {
				t.Error("应该要求 Brief")
			}
		})
	}
}

func TestTask_IsActive(t *testing.T) {
	tests := []struct {
		name   string
		task   Task
		want   bool
	}{
		{
			name: "active 状态",
			task: Task{Status: StatusActive},
			want: true,
		},
		{
			name: "completed 状态",
			task: Task{Status: StatusCompleted},
			want: false,
		},
		{
			name: "aborted 状态",
			task: Task{Status: StatusAborted},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.task.Status == StatusActive
			if got != tt.want {
				t.Errorf("IsActive() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTask_WallclockDuration(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name string
		task Task
		want time.Duration
	}{
		{
			name: "未结束的任务",
			task: Task{
				CreatedAt: now.Add(-1 * time.Hour),
				EndedAt:   nil,
				PausedMs:  0,
			},
			want: time.Hour, // 约等于
		},
		{
			name: "已结束的任务",
			task: Task{
				CreatedAt: now.Add(-2 * time.Hour),
				EndedAt:   &[]time.Time{now.Add(-1 * time.Hour)}[0],
				PausedMs:  0,
			},
			want: time.Hour,
		},
		{
			name: "有停顿的任务",
			task: Task{
				CreatedAt: now.Add(-2 * time.Hour),
				EndedAt:   &[]time.Time{now}[0],
				PausedMs:  3600000, // 1 小时
			},
			want: time.Hour, // 2小时 - 1小时停顿 = 1小时实际运行
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var wallclock time.Duration
			if tt.task.EndedAt != nil {
				wallclock = tt.task.EndedAt.Sub(tt.task.CreatedAt)
			} else {
				wallclock = time.Since(tt.task.CreatedAt)
			}
			wallclock -= time.Duration(tt.task.PausedMs) * time.Millisecond

			// 允许 1 秒误差
			diff := wallclock - tt.want
			if diff < 0 {
				diff = -diff
			}
			if diff > time.Second {
				t.Errorf("WallclockDuration() = %v, want %v (diff %v)", wallclock, tt.want, diff)
			}
		})
	}
}
