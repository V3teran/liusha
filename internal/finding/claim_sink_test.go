package finding

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/V3teran/liusha/internal/evaluator"
)

// errLookup 是任务行读取失败的替身错误。
var errLookup = errors.New("db down")

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
		Priority:     "high",
		Suggestion:   "对 search 参数做 HTML 实体编码输出，并启用 CSP 限制内联脚本。",
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

// 任务行读失败时不落账，错误原样交还：此刻按空主机写入，幂等会让空主机永久留在这条
// 记录上；留给补交付，下一次读到了主机再落账。
func TestClaimSink_TaskHostLookupFailureDefersDelivery(t *testing.T) {
	store := &fakeFindingStore{}
	sink := NewClaimSink(store, "run-1").WithTaskHostFallback(
		func(_ context.Context, _ string) (string, error) { return "", errLookup },
	)
	claim := evaluator.ConfirmedClaim{TaskID: "task-1", ResultNodeID: "n-1", Statement: "坐实"}
	err := sink.Record(context.Background(), claim)
	if err == nil || !strings.Contains(err.Error(), "读取任务目标主机") || !errors.Is(err, errLookup) {
		t.Fatalf("应交还读取失败的原因, got %v", err)
	}
	if len(store.saved) != 0 {
		t.Fatalf("读不到任务行时不该落账: %+v", store.saved)
	}
}

// 主机确实定位不到（坐实材料里没有地址，任务目标里也没有可识别的主机）：如实以空主机
// 入账——坐实了的发现不能因为定位不到主机而从台账里消失。
func TestClaimSink_DeliversWhenNoHostIsKnown(t *testing.T) {
	for name, fallback := range map[string]func(context.Context, string) (string, error){
		"没有兜底":   nil,
		"任务也没有":  func(context.Context, string) (string, error) { return "", nil },
		"任务标着未知": func(context.Context, string) (string, error) { return "unknown", nil },
	} {
		t.Run(name, func(t *testing.T) {
			store := &fakeFindingStore{}
			sink := NewClaimSink(store, "run-1")
			if fallback != nil {
				sink = sink.WithTaskHostFallback(fallback)
			}
			if err := sink.Record(context.Background(), evaluator.ConfirmedClaim{
				TaskID: "task-1", ResultNodeID: "n-1", Statement: "SSH 可用弱口令登录", HostHint: "unknown",
			}); err != nil {
				t.Fatalf("Record: %v", err)
			}
			if len(store.saved) != 1 || store.saved[0].Host != "" {
				t.Fatalf("应以空主机落账: %+v", store.saved)
			}
		})
	}
}

// 坐实材料里带地址时照旧抽得到主机，不依赖兜底。
func TestClaimSink_HostFromEvidenceBeforeFallback(t *testing.T) {
	store := &fakeFindingStore{}
	called := false
	sink := NewClaimSink(store, "run-1").WithTaskHostFallback(
		func(context.Context, string) (string, error) { called = true; return "fallback:80", nil },
	)
	claim := evaluator.ConfirmedClaim{
		TaskID: "task-1", ResultNodeID: "n-1", Statement: "坐实",
		Evidence: json.RawMessage(`{"url":"http://10.0.0.5:8080/login"}`),
	}
	if err := sink.Record(context.Background(), claim); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if called || store.saved[0].Host != "10.0.0.5:8080" {
		t.Fatalf("主机应取自证据里的地址: called=%v saved=%+v", called, store.saved)
	}
}

// 主张的陈述原样入账，不限长度与换行：曾有一条 562 字的 SQL 注入因为台账摘要的 500 字
// 上限永远交付不进去。
func TestClaimSink_StatementDeliveredVerbatim(t *testing.T) {
	store := &fakeFindingStore{}
	long := strings.Repeat("参数 id 未经过滤直接拼进 SQL；", 40) + "\n复现步骤：\n1. 提交 id=1 UNION SELECT …\n2. 返回了 users 表的全部行"
	if len([]rune(long)) <= 500 {
		t.Fatalf("测试陈述应超过 500 字, got %d", len([]rune(long)))
	}
	if err := NewClaimSink(store, "run-1").Record(context.Background(), evaluator.ConfirmedClaim{
		TaskID: "task-1", ResultNodeID: "n-1", Statement: long, HostHint: "example.com",
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if got := store.saved[0].Summary; got != long {
		t.Fatalf("陈述应一字不差入账:\n got %q\nwant %q", got, long)
	}
}

// 评级收进词表：词表内的大小写从宽，空值与词表之外的归缺省档——评级写得不对不能让
// 坐实了的主张进不了台账。
func TestClaimSink_SeverityNormalized(t *testing.T) {
	for in, want := range map[string]string{"critical": "critical", " HIGH ": "high", "info": "info", "": "medium", "P1": "medium"} {
		store := &fakeFindingStore{}
		if err := NewClaimSink(store, "run-1").Record(context.Background(), evaluator.ConfirmedClaim{
			TaskID: "task-1", ResultNodeID: "n-1", Statement: "坐实", Priority: in, HostHint: "example.com",
		}); err != nil {
			t.Fatalf("Record(%q): %v", in, err)
		}
		if got := store.saved[0].Severity; got != want {
			t.Fatalf("评级 %q 应归 %q, got %q", in, want, got)
		}
	}
}

// deriveTarget 对 steps 配方（recipe.steps 自由文本）也要抽出 method/path——
// 台账的定位列（方法、路径）不该因配方形状不同而缺失。
func TestDeriveTarget_StepsRecipe(t *testing.T) {
	steps := json.RawMessage(`{"kind":"steps","recipe":{"steps":"发送 GET /search?q=%3Cimg%3E\n用 traffic_view 抓取响应确认标记\n浏览器访问同一 URL"}}`)
	out := deriveTarget(steps)
	if out == nil {
		t.Fatal("steps 配方应抽出 target")
	}
	var tgt map[string]string
	if err := json.Unmarshal(out, &tgt); err != nil {
		t.Fatal(err)
	}
	if tgt["method"] != "GET" || tgt["path"] != "/search" {
		t.Fatalf("method/path 抽取不符: %v", tgt)
	}

	// 无 HTTP 请求行 → 诚实降级为 nil（不伪造）
	noReq := json.RawMessage(`{"kind":"steps","recipe":{"steps":"1. 登录 2. 提交 payload 3. 断言回显"}}`)
	if out := deriveTarget(noReq); out != nil {
		t.Fatalf("无可识别请求行的配方应返回 nil，得 %s", out)
	}
}
