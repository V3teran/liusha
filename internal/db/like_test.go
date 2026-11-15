package db

import "testing"

// 用户输入的 % _ \ 是字面字符，不是通配：转义后按字面匹配（PG 的 LIKE 缺省转义符是反斜杠）。
func TestEscapeLikeAndContains(t *testing.T) {
	cases := map[string]string{
		"user_id": `user\_id`,
		"100%":    `100\%`,
		`a\b`:     `a\\b`,
		"普通文本":    "普通文本",
	}
	for in, want := range cases {
		if got := EscapeLike(in); got != want {
			t.Errorf("EscapeLike(%q) = %q，期望 %q", in, got, want)
		}
	}
	if got := ContainsPattern(" user_id "); got != `%user\_id%` {
		t.Errorf("ContainsPattern 应去首尾空白并两端补 %%，得 %q", got)
	}
}
