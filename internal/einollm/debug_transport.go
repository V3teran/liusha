package einollm

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"time"
)

// debug_transport.go：LIUSHA_EINO_DEBUG_HTTP=1 时给 eino ChatModel 注入请求/响应体日志，
// 用于定位 provider 4xx（如小米 mimo「Param Incorrect」）—— 把实际发出的 messages JSON 打到 log。
//
// 默认关（newDebugHTTPClient 返 nil → factory 不注入 → 走 eino 默认 client）。

// debugHTTPEnabled 读环境开关（每次构造 model 时判，便于运行期开关不重启）。
func debugHTTPEnabled() bool {
	return os.Getenv("LIUSHA_EINO_DEBUG_HTTP") == "1"
}

// newDebugHTTPClient 在开关开时返回带 dump transport 的 *http.Client；否则 nil（用默认）。
func newDebugHTTPClient() *http.Client {
	if !debugHTTPEnabled() {
		return nil
	}
	return &http.Client{
		Timeout:   3 * time.Minute,
		Transport: &dumpTransport{inner: http.DefaultTransport},
	}
}

// dumpTransport 包一层 RoundTripper：成功的只记状态码，**失败（>=400）连请求体一起 dump**。
type dumpTransport struct {
	inner http.RoundTripper
}

func (d *dumpTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var reqBody []byte
	if req.Body != nil {
		reqBody, _ = io.ReadAll(req.Body)
		_ = req.Body.Close()
		req.Body = io.NopCloser(bytes.NewReader(reqBody))
	}

	resp, err := d.inner.RoundTrip(req)
	if err != nil {
		recorderLog.Warn().Err(err).Str("url", req.URL.String()).
			Str("req_body", string(reqBody)).Msg("eino HTTP roundtrip error（dump 请求体）")
		return resp, err
	}

	// 失败响应：dump 请求体 + 响应体（定位 Param Incorrect 等 4xx 的元凶 message）。
	if resp.StatusCode >= 400 {
		var respBody []byte
		if resp.Body != nil {
			respBody, _ = io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			resp.Body = io.NopCloser(bytes.NewReader(respBody))
		}
		recorderLog.Warn().
			Int("status", resp.StatusCode).
			Str("url", req.URL.String()).
			Str("resp_body", string(respBody)).
			Str("req_body", string(reqBody)).
			Msg("eino HTTP 4xx/5xx（dump 请求+响应体定位 provider 拒绝原因）")
	}
	return resp, nil
}
