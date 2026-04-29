// Package replay 负责按 Identity 替换 RawRequest 中的凭证位置（headers/query/body），
// 并通过受控并发把同一请求以多份身份重放出去；上层用它做授权类越权检测。
package replay

import (
	"net/http"
	"time"
)

// RawRequest 是从 http_flow 落库的原始请求快照，调用方不应在传入后再次修改它。
type RawRequest struct {
	Method  string
	URL     string
	Headers http.Header
	Body    []byte
}

// Response 是单次重放结果；按 Identity 维度回传，错误以 ErrorMessage 暴露给上层做归并。
type Response struct {
	IdentityName string
	StatusCode   int
	Headers      http.Header
	Body         []byte
	Latency      time.Duration
	ErrorMessage string
}
