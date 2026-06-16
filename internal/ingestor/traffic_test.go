package ingestor

import (
	"errors"
	"testing"

	"github.com/V3teran/liusha/internal/proxy"
)

// TestSubmitInternal_Backpressure 锁住进程内 internal 队列的背压契约：
// 非阻塞入队，队列满返 false（handler 据此回 503），有空位返 true。
func TestSubmitInternal_Backpressure(t *testing.T) {
	// 直接构造仅含 internalCh 的 Traffic（不走 NewTraffic，避免依赖 redis/stores）。
	capacity := 2
	tr := &Traffic{internalCh: make(chan *proxy.TrafficSnapshot, capacity)}

	// 填满
	for i := 0; i < capacity; i++ {
		if !tr.SubmitInternal(&proxy.TrafficSnapshot{HunterID: "h"}) {
			t.Fatalf("第 %d 次入队应成功（队列未满）", i)
		}
	}
	// 满了 → false（背压）
	if tr.SubmitInternal(&proxy.TrafficSnapshot{HunterID: "h"}) {
		t.Fatal("队列已满时 SubmitInternal 应返回 false")
	}
	// 取走一个腾出空位 → 又能入
	<-tr.internalCh
	if !tr.SubmitInternal(&proxy.TrafficSnapshot{HunterID: "h"}) {
		t.Fatal("腾出空位后 SubmitInternal 应返回 true")
	}
}

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
