package sitemap

import (
	"bytes"
	"compress/gzip"
	"strings"
	"testing"
)

func gzipBytes(s string) []byte {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write([]byte(s))
	_ = zw.Close()
	return buf.Bytes()
}

func TestExtractTitle(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want string
	}{
		{"普通 HTML", []byte("<html><head><title>用户管理</title></head><body>x</body></html>"), "用户管理"},
		{"大小写+属性", []byte(`<HTML><HEAD><TITLE lang="en">Reflected XSS</TITLE>`), "Reflected XSS"},
		{"实体解码", []byte("<title>Tom &amp; Jerry</title>"), "Tom & Jerry"},
		{"折叠多行空白", []byte("<title>\n  Vulnerability:\tReflected XSS  \n</title>"), "Vulnerability: Reflected XSS"},
		{"gzip 压缩", gzipBytes("<html><head><title>压缩页面</title></head>"), "压缩页面"},
		{"无 title（API/XHR）", []byte(`{"data":[1,2,3]}`), ""},
		{"空 body", nil, ""},
		{"无 head 的纯文本", []byte("OK"), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractTitle(tc.in); got != tc.want {
				t.Errorf("extractTitle = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestExtractTitleCapsLength(t *testing.T) {
	long := strings.Repeat("a", 200)
	got := extractTitle([]byte("<title>" + long + "</title>"))
	if len(got) != maxTitleLen {
		t.Errorf("title 未截断到 %d，实际 %d", maxTitleLen, len(got))
	}
}
