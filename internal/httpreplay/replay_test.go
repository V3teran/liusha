package httpreplay

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestReplay_TransportFailureIsMarked：没拿到目标响应（连接被拒 / 对端断开）必须带
// ErrTransport——调用方（复现门）据此把「目标不可达」与 LLM 侧瞬时故障、配方缺陷区分开。
func TestReplay_TransportFailureIsMarked(t *testing.T) {
	// 对端接受连接后不回任何字节直接断开 → EOF
	hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hj, _ := w.(http.Hijacker)
		c, _, _ := hj.Hijack()
		_ = c.Close()
	}))
	defer hang.Close()

	// 取一个已释放的端口 → connection refused
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	refused := "http://" + ln.Addr().String() + "/"
	_ = ln.Close()

	for name, u := range map[string]string{"EOF": hang.URL + "/search?q=x", "refused": refused} {
		t.Run(name, func(t *testing.T) {
			_, err := Replay(context.Background(), Source{Method: "GET", URL: u, Headers: json.RawMessage(`{}`)}, Overrides{})
			if !errors.Is(err, ErrTransport) {
				t.Fatalf("传输失败应带 ErrTransport，得 %v", err)
			}
		})
	}
}

// TestReplay_MalformedSourceIsNotTransport：请求本身构造不出来是确定性缺陷，不是传输失败。
func TestReplay_MalformedSourceIsNotTransport(t *testing.T) {
	_, err := Replay(context.Background(), Source{Method: "GET", URL: "http://127.0.0.1/", Headers: json.RawMessage(`[1,2]`)}, Overrides{})
	if err == nil || errors.Is(err, ErrTransport) {
		t.Fatalf("headers 非法应报错且不标传输失败，得 %v", err)
	}
}
