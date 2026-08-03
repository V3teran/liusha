package manifest

import "testing"

// fixture 覆盖三类：单域标签、多域标签、空标签（通用工具）。
func fixture() *Manifest {
	return &Manifest{Tools: []Tool{
		{Name: "nuclei", Category: "vulnscan", Scenarios: []string{"web"}},
		{Name: "trivy", Category: "vulnscan", Scenarios: []string{"cloud", "container"}},
		{Name: "curl", Category: "utility", Scenarios: nil}, // 空 = 通用，全域可见
	}}
}

// TestFilterByDomain_MatchesTaggedDomain 验证：按域过滤只留标了该域的工具 + 通用工具。
func TestFilterByDomain_MatchesTaggedDomain(t *testing.T) {
	got := fixture().FilterByDomain("web").Names()
	want := []string{"curl", "nuclei"} // trivy(cloud/container) 被过滤，curl 通用保留
	if !equal(got, want) {
		t.Fatalf("domain=web 期望 %v，得 %v", want, got)
	}
}

// TestFilterByDomain_MultiValueTag 验证：多域标签工具在其任一域下可见。
func TestFilterByDomain_MultiValueTag(t *testing.T) {
	got := fixture().FilterByDomain("cloud").Names()
	want := []string{"curl", "trivy"}
	if !equal(got, want) {
		t.Fatalf("domain=cloud 期望 %v，得 %v", want, got)
	}
}

// TestFilterByDomain_UnknownDomainKeepsUniversal 验证：未知域也保留通用工具（空标签）。
func TestFilterByDomain_UnknownDomainKeepsUniversal(t *testing.T) {
	got := fixture().FilterByDomain("ctf").Names()
	want := []string{"curl"}
	if !equal(got, want) {
		t.Fatalf("domain=ctf 期望仅通用工具 %v，得 %v", want, got)
	}
}

// TestFilterByDomain_EmptyDomainReturnsAll 验证：空 domain（未配置）不过滤，返回全集。
func TestFilterByDomain_EmptyDomainReturnsAll(t *testing.T) {
	got := fixture().FilterByDomain("").Names()
	want := []string{"curl", "nuclei", "trivy"}
	if !equal(got, want) {
		t.Fatalf("空 domain 期望全集 %v，得 %v", want, got)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
