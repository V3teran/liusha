package finding

import "testing"

// 评级收进五档词表：词表内的大小写与空白从宽，空值与词表之外的归缺省档 medium。
func TestNormalizeSeverity(t *testing.T) {
	cases := map[string]string{
		"critical": "critical", "HIGH": "high", " Low ": "low", "info": "info",
		"": "medium", "P1": "medium", "severe": "medium",
	}
	for in, want := range cases {
		if got := NormalizeSeverity(in); got != want {
			t.Errorf("NormalizeSeverity(%q) = %q, want %q", in, got, want)
		}
	}
	if Severities[2] != SeverityMedium {
		t.Fatalf("缺省档应是词表里由重到轻的第三档: %v", Severities)
	}
}
