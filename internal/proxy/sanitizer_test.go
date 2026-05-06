package proxy

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

func TestPatchAbsoluteURI(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "relative GET 用 Host header 拼 absolute",
			in:   "GET /api/x HTTP/1.1\r\nHost: vulnapp:8001\r\n\r\n",
			want: "GET http://vulnapp:8001/api/x HTTP/1.1\r\nHost: vulnapp:8001\r\n\r\n",
		},
		{
			name: "relative POST 含 body 仍 patch",
			in:   "POST /a HTTP/1.1\r\nHost: x\r\nContent-Length: 3\r\n\r\nabc",
			want: "POST http://x/a HTTP/1.1\r\nHost: x\r\nContent-Length: 3\r\n\r\nabc",
		},
		{
			name: "已是 absolute form 透传",
			in:   "GET http://x/y HTTP/1.1\r\nHost: x\r\n\r\n",
			want: "GET http://x/y HTTP/1.1\r\nHost: x\r\n\r\n",
		},
		{
			name: "https absolute form 透传",
			in:   "GET https://x/y HTTP/1.1\r\nHost: x\r\n\r\n",
			want: "GET https://x/y HTTP/1.1\r\nHost: x\r\n\r\n",
		},
		{
			name: "CONNECT 透传给 proxify 处理 TLS",
			in:   "CONNECT vulnapp:443 HTTP/1.1\r\nHost: vulnapp:443\r\n\r\n",
			want: "CONNECT vulnapp:443 HTTP/1.1\r\nHost: vulnapp:443\r\n\r\n",
		},
		{
			name: "缺 Host header 不强 patch（让 proxify 自己拒）",
			in:   "GET /x HTTP/1.1\r\nUser-Agent: curl\r\n\r\n",
			want: "GET /x HTTP/1.1\r\nUser-Agent: curl\r\n\r\n",
		},
		{
			name: "Host header 大小写不敏感",
			in:   "GET /x HTTP/1.1\r\nHOST: y\r\n\r\n",
			want: "GET http://y/x HTTP/1.1\r\nHOST: y\r\n\r\n",
		},
		{
			name: "畸形 request-line（缺空格）原样返回",
			in:   "GET\r\nHost: x\r\n\r\n",
			want: "GET\r\nHost: x\r\n\r\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := string(patchAbsoluteURI([]byte(c.in)))
			if got != c.want {
				t.Errorf("got=%q\nwant=%q", got, c.want)
			}
		})
	}
}

func TestExtractHostHeader(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"标准 Host", "Host: vulnapp:8001\r\n\r\n", "vulnapp:8001"},
		{"大写 HOST", "HOST: x\r\n\r\n", "x"},
		{"前置其他 header", "User-Agent: curl\r\nHost: x\r\n\r\n", "x"},
		{"无 Host", "User-Agent: curl\r\n\r\n", ""},
		{"前后有空格", "Host:   y   \r\n\r\n", "y"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := extractHostHeader([]byte(c.in))
			if got != c.want {
				t.Errorf("got=%q want=%q", got, c.want)
			}
		})
	}
}

func TestReadHTTPHead(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{
			name: "GET 无 body",
			in:   "GET /x HTTP/1.1\r\nHost: y\r\n\r\n",
			want: "GET /x HTTP/1.1\r\nHost: y\r\n\r\n",
		},
		{
			name: "POST head 部分（不读 body）",
			in:   "POST /x HTTP/1.1\r\nHost: y\r\nContent-Length: 3\r\n\r\nabc",
			want: "POST /x HTTP/1.1\r\nHost: y\r\nContent-Length: 3\r\n\r\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			br := bufio.NewReader(strings.NewReader(c.in))
			got, err := readHTTPHead(br)
			if (err != nil) != c.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, c.wantErr)
			}
			if !c.wantErr && !bytes.Equal(got, []byte(c.want)) {
				t.Errorf("got=%q\nwant=%q", got, c.want)
			}
		})
	}
}
