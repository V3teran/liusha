package ingestor

import (
	"errors"
	"testing"
)

// TestIsNoGroupErr 锁住 NOGROUP 自愈分支的入口判定。
// XREADGROUP 在 stream/group 不存在时返回类似：
//
//	"NOGROUP No such key 'liusha:flow_events' or consumer group 'liusha-ingestor' in XREADGROUP with GROUP option"
func TestIsNoGroupErr(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"NOGROUP from XREADGROUP", errors.New(
			"NOGROUP No such key 'liusha:flow_events' or consumer group 'liusha-ingestor' in XREADGROUP with GROUP option",
		), true},
		{"connection refused", errors.New("dial tcp 127.0.0.1:6379: connect: connection refused"), false},
		// BUSYGROUP 是自愈分支后续 XGROUP CREATE 可能命中的——不应被误判进入自愈循环
		{"BUSYGROUP", errors.New("BUSYGROUP Consumer Group name already exists"), false},
		// 大小写敏感：业界 redis 错误前缀是大写 NOGROUP，不做模糊匹配避免误判
		{"小写 nogroup 不匹配", errors.New("nogroup something"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isNoGroupErr(tc.err); got != tc.want {
				t.Errorf("isNoGroupErr(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
