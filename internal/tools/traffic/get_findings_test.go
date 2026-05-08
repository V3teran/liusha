package traffic

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/V3teran/liusha/internal/finding"
)

// fakeLister 实现 findingsLister 用于单测，避免拉起 PG。
type fakeLister struct {
	rows []finding.VulnFinding
	err  error
}

func (f *fakeLister) ListByEngagement(_ context.Context, _ string) ([]finding.VulnFinding, error) {
	return f.rows, f.err
}

// TestGetFindings_Output_NonEmpty 锁住核心 bug：Output 必须有内容，否则 LLM
// 看到的 tool message content 是空——会误判"未发现漏洞"。
func TestGetFindings_Output_NonEmpty(t *testing.T) {
	store := &fakeLister{rows: []finding.VulnFinding{
		{
			ID:         "f1",
			Kind:       "bac.unauthorized_access",
			Severity:   finding.SeverityCritical,
			Confidence: finding.ConfidenceHigh,
			Title:      "管理接口未授权",
		},
		{
			ID:         "f2",
			Kind:       "bac.horizontal_priv_esc",
			Severity:   finding.SeverityHigh,
			Confidence: finding.ConfidenceHigh,
			Title:      "用户可看他人订单",
		},
	}}
	gf := &GetFindings{Store: store, EngagementID: "eid-1"}

	res, err := gf.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(res.Output) == 0 {
		t.Fatal("Output 为空——LLM 会看到空 tool content，误判无漏洞")
	}

	var got struct {
		Count    int           `json:"count"`
		Findings []findingItem `json:"findings"`
	}
	if err := json.Unmarshal(res.Output, &got); err != nil {
		t.Fatalf("Output 不是合法 JSON: %v", err)
	}
	if got.Count != 2 || len(got.Findings) != 2 {
		t.Fatalf("count=%d findings=%d，期望 2/2", got.Count, len(got.Findings))
	}
	if got.Findings[0].Kind != "bac.unauthorized_access" {
		t.Errorf("kind=%q", got.Findings[0].Kind)
	}
	if got.Findings[0].Severity != "critical" || got.Findings[0].Confidence != "high" {
		t.Errorf("severity/confidence 字段缺失: %+v", got.Findings[0])
	}
	if !strings.Contains(res.Summary, "findings=2:") {
		t.Errorf("Summary 不含计数前缀: %q", res.Summary)
	}
}

// TestGetFindings_Empty 锁住空 finding 时 Output 仍是合法 JSON（不是空字符串）。
func TestGetFindings_Empty(t *testing.T) {
	gf := &GetFindings{Store: &fakeLister{}, EngagementID: "eid-1"}

	res, err := gf.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(res.Output) == 0 {
		t.Fatal("Output 为空——零 finding 时也应返回 {count:0,findings:[]}")
	}
	var got struct {
		Count    int           `json:"count"`
		Findings []findingItem `json:"findings"`
	}
	if err := json.Unmarshal(res.Output, &got); err != nil {
		t.Fatalf("Output 解析失败: %v", err)
	}
	if got.Count != 0 || got.Findings == nil || len(got.Findings) != 0 {
		t.Errorf("零 finding 应返回 count=0 findings=[]，实际 count=%d findings=%v", got.Count, got.Findings)
	}
}

// TestGetFindings_StoreError 锁住 Store 报错时透传——不要静默吞错。
func TestGetFindings_StoreError(t *testing.T) {
	gf := &GetFindings{Store: &fakeLister{err: errors.New("pg down")}, EngagementID: "eid-1"}

	if _, err := gf.Execute(context.Background(), nil); err == nil {
		t.Fatal("期望 store 错误透传，得到 nil")
	}
}

// TestGetFindings_MissingDeps 锁住装配错误立即报，避免下游 nil pointer。
func TestGetFindings_MissingDeps(t *testing.T) {
	cases := []struct {
		name string
		gf   *GetFindings
	}{
		{"nil store", &GetFindings{Store: nil, EngagementID: "x"}},
		{"empty eid", &GetFindings{Store: &fakeLister{}, EngagementID: ""}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.gf.Execute(context.Background(), nil); err == nil {
				t.Fatal("期望装配错误，得到 nil")
			}
		})
	}
}
