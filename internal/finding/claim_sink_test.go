package finding

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/V3teran/liusha/internal/evaluator"
)

// fakeFindingStore 记录落库的交付物。
type fakeFindingStore struct {
	saved []Finding
}

func (f *fakeFindingStore) Save(_ context.Context, v Finding) (Finding, error) {
	v.ID = "f-1"
	f.saved = append(f.saved, v)
	return v, nil
}

// 观察者在落库成功后收到已保存的交付物（会话漏洞卡的事件源）；
// 观察者 panic/缺位不影响交付本体（此处验证缺位与收到两个正向面）。
func TestClaimSink_ObserverReceivesSavedFinding(t *testing.T) {
	store := &fakeFindingStore{}
	var got Finding
	sink := NewClaimSink(store, "run-1").WithObserver(func(_ context.Context, f Finding) {
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

// 主机线索全部落空时回退任务锚定的目标主机（task.target_host）——缺这层
// 兜底，整条交付会被存储层以「finding.Host 必填」拒绝（线上曾整批丢弃）。
func TestClaimSink_TaskHostFallback(t *testing.T) {
	store := &fakeFindingStore{}
	sink := NewClaimSink(store, "run-1").WithTaskHostFallback(
		func(_ context.Context, taskID string) (string, error) {
			if taskID != "task-1" {
				t.Fatalf("taskID = %q, want task-1", taskID)
			}
			return "111.229.193.40:34280", nil
		},
	)
	claim := evaluator.ConfirmedClaim{TaskID: "task-1", ResultNodeID: "n-1", Statement: "坐实"}
	if err := sink.Record(context.Background(), claim); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if len(store.saved) != 1 || store.saved[0].Host != "111.229.193.40:34280" {
		t.Fatalf("Host 未走兜底: %+v", store.saved)
	}
}

// 兜底解析失败按无主机继续——交付交由存储校验拒绝（可观测），不报错不 panic。
func TestClaimSink_TaskHostFallbackErrorKeepsHostEmpty(t *testing.T) {
	store := &fakeFindingStore{}
	sink := NewClaimSink(store, "run-1").WithTaskHostFallback(
		func(_ context.Context, _ string) (string, error) { return "", errors.New("db down") },
	)
	claim := evaluator.ConfirmedClaim{TaskID: "task-1", ResultNodeID: "n-1"}
	if err := sink.Record(context.Background(), claim); err != nil {
		t.Fatalf("Record 不应因兜底失败报错: %v", err)
	}
	if len(store.saved) != 1 || store.saved[0].Host != "" {
		t.Fatalf("Host 应保持空: %+v", store.saved)
	}
}
