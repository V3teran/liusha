package einotools

import (
	"net/url"
	"strings"
	"testing"
)

func strptr(s string) *string { return &s }

func TestApplyQueryMods(t *testing.T) {
	cases := []struct {
		name, in string
		mods     map[string]*string
		want     map[string]string // 期望的 query 键值（按解析后比对，避免编码顺序）
		gone     []string          // 期望已删除的键
	}{
		{"新增参数", "http://h/p?a=1", map[string]*string{"b": strptr("2")},
			map[string]string{"a": "1", "b": "2"}, nil},
		{"改 IDOR id", "http://h/p?id=1", map[string]*string{"id": strptr("2")},
			map[string]string{"id": "2"}, nil},
		{"删 token 测未授权", "http://h/p?id=1&token=abc", map[string]*string{"token": nil},
			map[string]string{"id": "1"}, []string{"token"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := applyQueryMods(c.in, c.mods)
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			u, _ := url.Parse(got)
			q := u.Query()
			for k, v := range c.want {
				if q.Get(k) != v {
					t.Errorf("query[%s]=%q，期望 %q（url=%s）", k, q.Get(k), v, got)
				}
			}
			for _, k := range c.gone {
				if q.Has(k) {
					t.Errorf("query[%s] 应已删除（url=%s）", k, got)
				}
			}
		})
	}
}

func TestApplyBodyFieldMods(t *testing.T) {
	t.Run("form 改字段", func(t *testing.T) {
		got, err := applyBodyFieldMods([]byte("a=1&b=2"), "application/x-www-form-urlencoded",
			map[string]*string{"a": strptr("9")})
		if err != nil {
			t.Fatal(err)
		}
		v, _ := url.ParseQuery(string(got))
		if v.Get("a") != "9" || v.Get("b") != "2" {
			t.Errorf("got %q，期望 a=9 且 b=2", got)
		}
	})

	t.Run("form 删字段（测未授权）", func(t *testing.T) {
		got, err := applyBodyFieldMods([]byte("user=bob&csrf=tok"), "application/x-www-form-urlencoded",
			map[string]*string{"csrf": nil})
		if err != nil {
			t.Fatal(err)
		}
		v, _ := url.ParseQuery(string(got))
		if v.Has("csrf") || v.Get("user") != "bob" {
			t.Errorf("got %q，期望删掉 csrf 保留 user=bob", got)
		}
	})

	t.Run("JSON 改字段保留数字类型", func(t *testing.T) {
		got, err := applyBodyFieldMods([]byte(`{"id":1,"name":"x"}`), "application/json",
			map[string]*string{"id": strptr("5")})
		if err != nil {
			t.Fatal(err)
		}
		// "5" 是合法 JSON → 应作数字写入（无引号）
		if !strings.Contains(string(got), `"id":5`) {
			t.Errorf("got %q，期望 \"id\":5（数字非字符串）", got)
		}
	})

	t.Run("JSON 非法 JSON 值当字符串", func(t *testing.T) {
		got, err := applyBodyFieldMods([]byte(`{"role":"user"}`), "application/json",
			map[string]*string{"role": strptr("admin")})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(got), `"role":"admin"`) {
			t.Errorf("got %q，期望 \"role\":\"admin\"（字符串）", got)
		}
	})

	t.Run("不支持的 content-type 报错", func(t *testing.T) {
		_, err := applyBodyFieldMods([]byte("<x/>"), "text/xml",
			map[string]*string{"a": strptr("1")})
		if err == nil {
			t.Error("text/xml 应报错让 LLM 改用整体 body 替换")
		}
	})
}
