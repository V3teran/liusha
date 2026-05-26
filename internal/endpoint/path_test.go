package endpoint

import "testing"

func TestTemplatizePath(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		// 根 path 不能被 trim 成空
		{"/", "/"},
		// 尾斜杠规范化（避免 commander/striker 之间 /x vs /x/ 分裂）
		{"/vulnerabilities/xss_d/", "/vulnerabilities/xss_d"},
		{"/vulnerabilities/xss_d", "/vulnerabilities/xss_d"},
		{"/foo/bar//", "/foo/bar"},
		// 数字段
		{"/user/1", "/user/:id"},
		{"/user/12345/posts", "/user/:id/posts"},
		// UUID
		{"/api/u/550e8400-e29b-41d4-a716-446655440000", "/api/u/:uuid"},
		// 长 hex
		{"/api/file/abcdef0123456789abcd", "/api/file/:hex"},
		// 短 hex 不替换（< 16）
		{"/api/file/abc123", "/api/file/abc123"},
	}
	for _, tc := range cases {
		got := TemplatizePath(tc.in)
		if got != tc.want {
			t.Errorf("TemplatizePath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
