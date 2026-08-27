package web

import (
	"encoding/json"
	"testing"

	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/worldmodel"
)

func TestAttemptFromFinding_WithRepro(t *testing.T) {
	f := finding.VulnFinding{
		ID:            "f-1",
		Seq:           7,
		Host:          "t.local",
		Severity:      "high",
		Summary:       "IDOR - order id 越权",
		CWEID:         "CWE-639",
		OWASPCategory: "A01:2021",
		Target:        json.RawMessage(`{"path":"/api/order","method":"GET"}`),
		Repro:         json.RawMessage(`{"traffic_id":42,"assert":{"status_code":200}}`),
	}
	a, ok, err := AttemptFromFinding("scan-1", f)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("带 repro 的 finding 应可晋升")
	}
	if a.TaskID != "scan-1" {
		t.Errorf("TaskID=%q，期望 scan-1", a.TaskID)
	}
	if a.Kind != worldmodel.KindDiscovery {
		t.Errorf("Kind=%q，期望 finding", a.Kind)
	}
	if a.Target.Domain != "web" || a.Target.RefKind != "endpoint" {
		t.Errorf("Target 应为 web/endpoint，得 %+v", a.Target)
	}
	if a.Target.Locator != "t.local/api/order" {
		t.Errorf("Locator=%q，期望 t.local/api/order", a.Target.Locator)
	}
	// Primitives 原样透传 repro（Verifier 侧解析）。
	if string(a.Primitives) != `{"traffic_id":42,"assert":{"status_code":200}}` {
		t.Errorf("Primitives 应原样透传 repro，得 %s", a.Primitives)
	}
	// Attrs 回指 finding 元数据。
	var attrs findingAttrs
	if err := json.Unmarshal(a.Attrs, &attrs); err != nil {
		t.Fatalf("Attrs 应为合法 JSON: %v", err)
	}
	if attrs.FindingID != "f-1" || attrs.Seq != 7 || attrs.Severity != "high" || attrs.CWEID != "CWE-639" {
		t.Errorf("Attrs 元数据失真: %+v", attrs)
	}
}

func TestAttemptFromFinding_NoRepro_Skips(t *testing.T) {
	for _, tc := range []struct {
		name  string
		repro json.RawMessage
	}{
		{"nil repro", nil},
		{"空 object", json.RawMessage(`{}`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := finding.VulnFinding{ID: "f-2", Host: "t.local", Summary: "无配方漏洞", Repro: tc.repro}
			_, ok, err := AttemptFromFinding("scan-1", f)
			if err != nil {
				t.Fatal(err)
			}
			if ok {
				t.Fatal("无复现配方的 finding 不应晋升")
			}
		})
	}
}

func TestEndpointRef_NoPath_FallsBackToHost(t *testing.T) {
	f := finding.VulnFinding{ID: "f-3", Host: "t.local", Summary: "站点级", Repro: json.RawMessage(`{"traffic_id":1,"assert":{"status_code":500}}`)}
	a, ok, _ := AttemptFromFinding("scan-1", f)
	if !ok {
		t.Fatal("应可晋升")
	}
	if a.Target.Locator != "t.local" {
		t.Errorf("无 path 应退化为纯 host，得 %q", a.Target.Locator)
	}
}

func TestEndpointRef_MalformedTarget_FallsBackToHost(t *testing.T) {
	f := finding.VulnFinding{ID: "f-4", Host: "t.local", Summary: "坏 target", Target: json.RawMessage(`not json`), Repro: json.RawMessage(`{"traffic_id":1,"assert":{"status_code":200}}`)}
	a, ok, _ := AttemptFromFinding("scan-1", f)
	if !ok {
		t.Fatal("应可晋升")
	}
	if a.Target.Locator != "t.local" {
		t.Errorf("坏 target 应退化为纯 host，得 %q", a.Target.Locator)
	}
}
