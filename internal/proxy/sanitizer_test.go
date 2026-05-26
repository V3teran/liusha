package proxy

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
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

func TestExtractHunterIDFromHead(t *testing.T) {
	mkAuth := func(user, pass string) string {
		return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
	}
	const uuid = "8e256a52-5cc6-403b-9d23-a4a788fd5b80"

	cases := []struct {
		name string
		head string
		want string
	}{
		{
			name: "有效 hunter_<uuid> 凭证",
			head: "GET / HTTP/1.1\r\nHost: x\r\nProxy-Authorization: " + mkAuth("hunter_"+uuid, "_") + "\r\n\r\n",
			want: uuid,
		},
		{
			name: "缺 Proxy-Authorization → 空",
			head: "GET / HTTP/1.1\r\nHost: x\r\n\r\n",
			want: "",
		},
		{
			name: "Proxy-Authorization 但不是 hunter_ 前缀 → 空",
			head: "GET / HTTP/1.1\r\nHost: x\r\nProxy-Authorization: " + mkAuth("anon", "_") + "\r\n\r\n",
			want: "",
		},
		{
			name: "header case-insensitive 仍能识别",
			head: "GET / HTTP/1.1\r\nHost: x\r\nPROXY-AUTHORIZATION: " + mkAuth("hunter_"+uuid, "_") + "\r\n\r\n",
			want: uuid,
		},
		{
			name: "Basic 后乱码 → 空（不 panic）",
			head: "GET / HTTP/1.1\r\nHost: x\r\nProxy-Authorization: Basic !!!notbase64!!!\r\n\r\n",
			want: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := extractHunterIDFromHead([]byte(c.head)); got != c.want {
				t.Errorf("got=%q want=%q", got, c.want)
			}
		})
	}
}

// TestForwardWithRewrite_407Challenge：require_auth=true + 缺 auth → 407 challenge。
// 用 net.Pipe 模拟 client；upstream 不应被 dial（auth 失败早返）。
func TestForwardWithRewrite_407Challenge(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		// upstreamAddr 给个 unreachable 地址；require_auth 应该早返根本不 dial
		forwardWithRewrite(serverConn, "127.0.0.1:1", true)
	}()

	// 写无 auth 的请求
	if _, err := clientConn.Write([]byte("GET / HTTP/1.1\r\nHost: x\r\n\r\n")); err != nil {
		t.Fatalf("write: %v", err)
	}

	// 读响应
	_ = clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	resp, err := io.ReadAll(clientConn)
	if err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("read: %v", err)
	}
	if !strings.HasPrefix(string(resp), "HTTP/1.1 407") {
		t.Errorf("resp=%q want HTTP/1.1 407 prefix", resp)
	}
	if !strings.Contains(string(resp), "Proxy-Authenticate: Basic") {
		t.Errorf("resp=%q missing Proxy-Authenticate", resp)
	}

	<-done
}

// TestForwardWithRewrite_AuthOK：require_auth=true + 带 hunter_ auth → 转发上游。
func TestForwardWithRewrite_AuthOK(t *testing.T) {
	// 起本地 upstream listener，收到的请求 echo 回去校验
	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen upstream: %v", err)
	}
	defer upstream.Close()

	upstreamGot := make(chan []byte, 1)
	go func() {
		c, err := upstream.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		buf := make([]byte, 4096)
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, _ := c.Read(buf)
		upstreamGot <- buf[:n]
	}()

	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	go forwardWithRewrite(serverConn, upstream.Addr().String(), true)

	const uuid = "8e256a52-5cc6-403b-9d23-a4a788fd5b80"
	auth := "Basic " + base64.StdEncoding.EncodeToString([]byte("hunter_"+uuid+":_"))
	req := "GET / HTTP/1.1\r\nHost: x\r\nProxy-Authorization: " + auth + "\r\n\r\n"
	if _, err := clientConn.Write([]byte(req)); err != nil {
		t.Fatalf("write: %v", err)
	}

	select {
	case got := <-upstreamGot:
		s := string(got)
		if !strings.Contains(s, "X-Liusha-Hunter-Id: "+uuid) {
			t.Errorf("upstream got=%q missing X-Liusha-Hunter-Id: %s", s, uuid)
		}
		if strings.Contains(strings.ToLower(s), "proxy-authorization:") {
			t.Errorf("upstream got=%q still contains Proxy-Authorization", s)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("upstream 超时未收到请求")
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
