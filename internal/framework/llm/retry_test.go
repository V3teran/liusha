package llm

import (
	"context"
	"fmt"
	"testing"
)

// TestClassifyDNSTransient 回归：DNS 解析失败是瞬时网络错误，必须可重试。
// e2e 实测：GLM 端点 DNS 抖动 35 次全部被误判为"不可重试错误"——三处分类器
// 词表各自漂移的系统性缺陷，现统一委托 llm.Classify。
func TestClassifyDNSTransient(t *testing.T) {
	dnsErr := fmt.Errorf(`Post "https://open.bigmodel.cn/api/paas/v4/chat/completions": dial tcp: lookup open.bigmodel.cn: no such host`)
	if Classify(dnsErr) != ClassNet {
		t.Fatalf("DNS 解析失败应判为 ClassNet，got %v", Classify(dnsErr))
	}
	if !IsTransient(dnsErr) {
		t.Fatal("DNS 解析失败应为瞬时错误")
	}

	cases := []struct {
		name string
		err  error
		want ErrorClass
	}{
		{"dial tcp refused", fmt.Errorf(`dial tcp 1.2.3.4:443: connect: connection refused`), ClassNet},
		{"network unreachable", fmt.Errorf(`dial tcp: lookup api.example: no such host`), ClassNet},
		{"ctx deadline", context.DeadlineExceeded, ClassNet},
		{"ctx canceled not transient", context.Canceled, ClassOther},
		{"http timeout", fmt.Errorf(`Get "http://x": context deadline exceeded (Client.Timeout exceeded while awaiting headers)`), ClassNet},
		{"rate limit", fmt.Errorf("http 429: Too Many Requests"), Class429},
		{"nil", nil, ClassOther},
		{"auth error permanent", fmt.Errorf("http 401: unauthorized"), ClassOther},
	}
	for _, tc := range cases {
		if got := Classify(tc.err); got != tc.want {
			t.Errorf("%s: Classify=%v want %v", tc.name, got, tc.want)
		}
	}
}
