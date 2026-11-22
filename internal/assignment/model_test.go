package assignment

import "testing"

func TestSource_String(t *testing.T) {
	tests := []struct {
		source Source
		want   string
	}{
		{SourceDirect, "direct"},
		{SourceProxy, "proxy"},
	}
	for _, tt := range tests {
		t.Run(string(tt.source), func(t *testing.T) {
			if got := string(tt.source); got != tt.want {
				t.Errorf("Source = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStatus_String(t *testing.T) {
	tests := []struct {
		status Status
		want   string
	}{
		{StatusEmpty, "empty"},
		{StatusRunning, "running"},
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
