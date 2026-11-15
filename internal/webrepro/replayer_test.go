package webrepro

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/V3teran/liusha/internal/evaluator"
)

// 复现通道用哨兵错误告诉中立的复现门失败属于哪一类：配方缺陷（一次出圈）、目标不可达
// （计入重试阶梯）。门与评估者只认哨兵，不认本领域的错误文本。
func TestReplay_ClassifiesFailuresWithSentinels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("leaked-secret"))
	}))
	defer srv.Close()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("占用端口失败: %v", err)
	}
	closedURL := "http://" + ln.Addr().String() + "/x"
	_ = ln.Close() // 端口释放后无人监听：连接被拒

	envelope := func(url, assert string) json.RawMessage {
		return json.RawMessage(`{"domain":"web","recipe":{"request":{"method":"GET","url":"` + url + `","headers":{},"body":""}},"assert":` + assert + `}`)
	}
	cases := []struct {
		name    string
		in      json.RawMessage
		wantErr error
	}{
		{"信封不是对象", json.RawMessage(`[1]`), evaluator.ErrInvalidRecipe},
		{"别的领域的配方", json.RawMessage(`{"domain":"generic","recipe":{"steps":"s"},"assert":{}}`), evaluator.ErrInvalidRecipe},
		{"无坐实谓词", envelope(srv.URL, `{}`), evaluator.ErrInvalidRecipe},
		{"相对 URL", envelope("/rel", `{"status_code":200}`), evaluator.ErrInvalidRecipe},
		{"目标不可达", envelope(closedURL, `{"body_contains":["leaked-secret"]}`), evaluator.ErrTargetUnreachable},
	}
	r := NewReplayer()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := r.Replay(context.Background(), tc.in); !errors.Is(err, tc.wantErr) {
				t.Fatalf("期望 %v，得 %v", tc.wantErr, err)
			}
		})
	}

	res, err := r.Replay(context.Background(), envelope(srv.URL, `{"body_contains":["leaked-secret"]}`))
	if err != nil {
		t.Fatalf("可达目标的合法配方应重放成功: %v", err)
	}
	var ev struct {
		AssertPassed bool `json:"assert_passed"`
	}
	if err := json.Unmarshal(res.Evidence, &ev); err != nil || !ev.AssertPassed {
		t.Fatalf("断言应命中: %s err=%v", res.Evidence, err)
	}
}
