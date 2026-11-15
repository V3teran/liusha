package sandbox

import "testing"

// TestParseListenerPorts：监听段配置解析的边界——空=禁用、合法段、格式/范围/宽度拒绝。
func TestParseListenerPorts(t *testing.T) {
	cases := []struct {
		spec    string
		start   int
		count   int
		wantErr bool
	}{
		{"", 0, 0, false},
		{"   ", 0, 0, false},
		{"21000-21099", 21000, 100, false},
		{" 21000 - 21099 ", 21000, 100, false},
		{"4444-4444", 4444, 1, false},
		{"21000", 0, 0, true},
		{"a-b", 0, 0, true},
		{"0-100", 0, 0, true},
		{"70000-70001", 0, 0, true},
		{"21099-21000", 0, 0, true},
		{"10000-20000", 0, 0, true}, // 宽度 10001 > 1000
	}
	for _, c := range cases {
		start, count, err := ParseListenerPorts(c.spec)
		if c.wantErr {
			if err == nil {
				t.Errorf("%q 应拒绝", c.spec)
			}
			continue
		}
		if err != nil || start != c.start || count != c.count {
			t.Errorf("%q => (%d,%d,%v), want (%d,%d,nil)", c.spec, start, count, err, c.start, c.count)
		}
	}
}
