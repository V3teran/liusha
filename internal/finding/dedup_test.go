package finding

import "testing"

// TestNormalizeDedupKey 验证 dedup_key path 模板化覆盖典型 ID 形式。
func TestNormalizeDedupKey(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "纯数字 ID",
			in:   "bac.horizontal_priv_esc:vulnapp:GET:/api/order/12345",
			want: "bac.horizontal_priv_esc:vulnapp:GET:/api/order/:id",
		},
		{
			name: "短数字 ID",
			in:   "bac.horizontal_priv_esc:vulnapp:GET:/api/order/7",
			want: "bac.horizontal_priv_esc:vulnapp:GET:/api/order/:id",
		},
		{
			name: "UUID hyphenated",
			in:   "bac.horizontal_priv_esc:vulnapp:GET:/api/file/550e8400-e29b-41d4-a716-446655440000",
			want: "bac.horizontal_priv_esc:vulnapp:GET:/api/file/:uuid",
		},
		{
			name: "UUID hex32",
			in:   "bac.horizontal_priv_esc:vulnapp:GET:/api/file/550e8400e29b41d4a716446655440000",
			want: "bac.horizontal_priv_esc:vulnapp:GET:/api/file/:uuid",
		},
		{
			name: "长 hex 串（SHA1）",
			in:   "bac.horizontal_priv_esc:vulnapp:GET:/api/sha/abc123def4567890abcd",
			want: "bac.horizontal_priv_esc:vulnapp:GET:/api/sha/:hex",
		},
		{
			name: "纯字面量 path 不变",
			in:   "bac.unauthorized_access:vulnapp:POST:/api/admin/delete",
			want: "bac.unauthorized_access:vulnapp:POST:/api/admin/delete",
		},
		{
			name: "嵌套数字段全部模板化",
			in:   "bac.horizontal_priv_esc:vulnapp:GET:/api/user/123/order/456",
			want: "bac.horizontal_priv_esc:vulnapp:GET:/api/user/:id/order/:id",
		},
		{
			name: "短 hex（< 16 位）不算 :hex",
			in:   "bac.horizontal_priv_esc:vulnapp:GET:/api/x/abc",
			want: "bac.horizontal_priv_esc:vulnapp:GET:/api/x/abc",
		},
		{
			name: "保留 query string",
			in:   "bac.horizontal_priv_esc:vulnapp:GET:/api/order/12345?foo=bar",
			want: "bac.horizontal_priv_esc:vulnapp:GET:/api/order/:id?foo=bar",
		},
		{
			name: "空 dedup_key 原样返回",
			in:   "",
			want: "",
		},
		{
			name: "格式不符（少于 4 段）原样返回",
			in:   "kind:host",
			want: "kind:host",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := NormalizeDedupKey(tc.in)
			if got != tc.want {
				t.Errorf("NormalizeDedupKey(%q)\n got=%q\nwant=%q", tc.in, got, tc.want)
			}
		})
	}
}

// TestTemplatizePath 直接测 path-only 函数。
func TestTemplatizePath(t *testing.T) {
	cases := map[string]string{
		"/api/order/7":                                   "/api/order/:id",
		"/api/file/550e8400-e29b-41d4-a716-446655440000": "/api/file/:uuid",
		"/api/admin/users":                               "/api/admin/users",
		"/api/sha/abc123def4567890abcd":                  "/api/sha/:hex",
		"":                                               "",
	}
	for in, want := range cases {
		if got := TemplatizePath(in); got != want {
			t.Errorf("TemplatizePath(%q)\n got=%q\nwant=%q", in, got, want)
		}
	}
}
