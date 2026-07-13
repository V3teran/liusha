package main

import "testing"

func TestParseDistilled(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantLen int
		wantErr bool
	}{
		{"空数组", "[]", 0, false},
		{"空串", "  ", 0, false},
		{"裸 JSON", `[{"title":"t","content":"c","tags":["x"]}]`, 1, false},
		{"代码块包裹", "```json\n[{\"title\":\"t\",\"content\":\"c\"}]\n```", 1, false},
		{"两条", `[{"title":"a","content":"1"},{"title":"b","content":"2"}]`, 2, false},
		{"坏 JSON", `{not json`, 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseDistilled(c.raw)
			if (err != nil) != c.wantErr {
				t.Fatalf("err=%v，期望 wantErr=%v", err, c.wantErr)
			}
			if !c.wantErr && len(got) != c.wantLen {
				t.Fatalf("条数=%d，期望 %d", len(got), c.wantLen)
			}
		})
	}
}
