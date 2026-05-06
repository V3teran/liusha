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

// OriginalIdentityName 是注入到响应列表中的"原始抓包响应"伪身份名。
// 下划线包裹避免与真实身份名（admin / anonymous / user）冲突；
// 启发式与相似度规则以它为锚点判断"非授权身份是否拿到了原用户能看到的数据"。
const OriginalIdentityName = "_original_"

// Response 是单次重放结果；按 Identity × Variant 维度回传，错误以 ErrorMessage 暴露给上层做归并。
//
// VariantName 是请求变体的标识：
//   - BAC 场景：仅"原样重放"，所有响应 VariantName=BaselineVariantName
//   - SQLi 场景：identity=admin × variants=[baseline, err_quote, bool_true, ...]，VariantName 标记具体 payload
type Response struct {
	IdentityName string
	VariantName  string
	StatusCode   int
	Headers      http.Header
	Body         []byte
	Latency      time.Duration
	ErrorMessage string
}
