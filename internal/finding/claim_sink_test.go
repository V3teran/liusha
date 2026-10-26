package finding

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/V3teran/liusha/internal/evaluator"
)

// fakeFindingStore 记录落库的交付物。
type fakeFindingStore struct {
	saved []VulnFinding
}

func (f *fakeFindingStore) Save(_ context.Context, v VulnFinding) (VulnFinding, error) {
	v.ID = "f-1"
	f.saved = append(f.saved, v)
	return v, nil
}

// 观察者在落库成功后收到已保存的交付物（会话漏洞卡的事件源）；
// 观察者 panic/缺位不影响交付本体（此处验证缺位与收到两个正向面）。
func TestClaimSink_ObserverReceivesSavedFinding(t *testing.T) {
	store := &fakeFindingStore{}
	var got VulnFinding
	sink := NewClaimSink(store, "run-1").WithObserver(func(_ context.Context, f VulnFinding) {
		got = f
	})

	err := sink.Record(context.Background(), evaluator.ConfirmedClaim{
		TaskID:       "t-1",
		Statement:    "反射型 XSS：search 参数未过滤",
		Severity:     "high",
		Remediation:  "对 search 参数做 HTML 实体编码输出，并启用 CSP 限制内联脚本。",
		HostHint:     "example.com",
		Evidence:     json.RawMessage(`{}`),
		ResultNodeID: "n-1",
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if len(store.saved) != 1 {
		t.Fatalf("应落一条交付物, got %d", len(store.saved))
	}
	if got.Summary != "反射型 XSS：search 参数未过滤" || got.Severity != "high" || got.Host != "example.com" {
		t.Fatalf("基础字段映射: %+v", got)
	}
	if got.Remediation != "对 search 参数做 HTML 实体编码输出，并启用 CSP 限制内联脚本。" {
		t.Fatalf("修复建议应随主张落台账: %q", got.Remediation)
		t.Fatalf("观察者收到 = %+v", got)
	}
	if got.ID != "f-1" {
		t.Fatalf("观察者应收到已保存（含回填 ID）的交付物, got ID=%q", got.ID)
	}
}

// 无观察者：行为不变。
func TestClaimSink_NoObserver(t *testing.T) {
	store := &fakeFindingStore{}
	sink := NewClaimSink(store, "run-1")
	if err := sink.Record(context.Background(), evaluator.ConfirmedClaim{TaskID: "t-1", Statement: "s"}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if len(store.saved) != 1 {
		t.Fatalf("无观察者不应影响落库, got %d", len(store.saved))
	}
}
