package attackgraph

import (
	"testing"

	"github.com/V3teran/liusha/internal/finding"
)

func mkFinding(id, host, sev, summary string, deps ...string) finding.VulnFinding {
	return finding.VulnFinding{ID: id, Host: host, Severity: sev, Summary: summary, DependsOn: deps}
}

func TestFindingSubgraph(t *testing.T) {
	tests := []struct {
		name      string
		findings  []finding.VulnFinding
		wantNodes int
		wantEdges []Edge
	}{
		{
			name:      "空输入返回空图",
			findings:  nil,
			wantNodes: 0,
			wantEdges: nil,
		},
		{
			name: "无依赖只出节点不出边",
			findings: []finding.VulnFinding{
				mkFinding("a", "h1", "high", "SQL 注入"),
				mkFinding("b", "h1", "low", "信息泄露"),
			},
			wantNodes: 2,
			wantEdges: nil,
		},
		{
			name: "组合漏洞派生两条依赖边",
			findings: []finding.VulnFinding{
				mkFinding("a", "h1", "medium", "任意文件上传"),
				mkFinding("b", "h1", "medium", "路径穿越"),
				mkFinding("c", "h1", "critical", "上传+穿越=RCE", "a", "b"),
			},
			wantNodes: 3,
			wantEdges: []Edge{
				{From: "a", To: "c", Type: EdgeDependsOn},
				{From: "b", To: "c", Type: EdgeDependsOn},
			},
		},
		{
			name: "自引用与悬空依赖被跳过",
			findings: []finding.VulnFinding{
				mkFinding("a", "h1", "high", "自己依赖自己", "a"),
				mkFinding("b", "h1", "high", "依赖不存在的 x", "x"),
			},
			wantNodes: 2,
			wantEdges: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := FindingSubgraph("owner-1", tt.findings)

			if g.OwnerID != "owner-1" {
				t.Errorf("OwnerID = %q, 期望 owner-1", g.OwnerID)
			}
			if len(g.Nodes) != tt.wantNodes {
				t.Errorf("节点数 = %d, 期望 %d", len(g.Nodes), tt.wantNodes)
			}
			if len(g.Edges) != len(tt.wantEdges) {
				t.Fatalf("边数 = %d, 期望 %d (edges=%+v)", len(g.Edges), len(tt.wantEdges), g.Edges)
			}
			for i, want := range tt.wantEdges {
				if g.Edges[i] != want {
					t.Errorf("边[%d] = %+v, 期望 %+v", i, g.Edges[i], want)
				}
			}
		})
	}
}

func TestFindingSubgraphNodeFields(t *testing.T) {
	findings := []finding.VulnFinding{
		mkFinding("a", "example.com:8080", "high", "首行标题\n第二行细节应被截掉"),
	}
	g := FindingSubgraph("o", findings)
	if len(g.Nodes) != 1 {
		t.Fatalf("节点数 = %d, 期望 1", len(g.Nodes))
	}
	n := g.Nodes[0]
	if n.Kind != KindFinding {
		t.Errorf("Kind = %q, 期望 %q", n.Kind, KindFinding)
	}
	if n.Target != "example.com:8080" {
		t.Errorf("Target = %q, 期望保留 host:port", n.Target)
	}
	if n.Ref != "a" {
		t.Errorf("Ref = %q, 期望 finding id 指针", n.Ref)
	}
	if n.Title != "首行标题" {
		t.Errorf("Title = %q, 期望仅首行", n.Title)
	}
}
