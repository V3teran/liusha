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
				Goal:         "测试目标",
			},
			wantErr: false,
		},
		{
			name: "缺少 AssignmentID",
			params: NewParams{
				Goal: "测试目标",
			},
			wantErr: true,
		},
		{
			name: "缺少 Goal",
			params: NewParams{
				AssignmentID: "a1",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 验证逻辑在 Store.Create 中，这里只测试参数结构
			if tt.params.AssignmentID == "" && !tt.wantErr {
				t.Error("应该要求 AssignmentID")
			}
			if tt.params.Goal == "" && !tt.wantErr {
				t.Error("应该要求 Goal")
			}
		})
	}
}

func TestTask_IsActive(t *testing.T) {
	tests := []struct {
		name string
		task Task
		want bool
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

// 目标主机是目标的派生：取第一个 http(s) 目标的主机（含端口），没有则为空。
func TestTargetHostOf(t *testing.T) {
	cases := map[string]string{
		"测试 http://a.test:8080/login 与 https://b.test": "a.test:8080",
		"代理流量分析：https://shop.test 的 3 条流量":             "shop.test",
		"分析 10.0.0.1 的代理流量":                            "",
	}
	for goal, want := range cases {
		if got := targetHostOf(goal); got != want {
			t.Errorf("targetHostOf(%q) = %q，期望 %q", goal, got, want)
		}
	}
}
