package llm

import (
	"errors"
	"net/http"
	"strings"
	"sync/atomic"

	openai "github.com/sashabaranov/go-openai"
)

// requestCompat 是生成器对上游「可选请求字段」的兼容结论。OpenAI 兼容协议下各服务
// 对字段的接受度不一：多数认 max_tokens，部分推理模型只认 max_completion_tokens；
// 部分模型不接受自定义 temperature。结论来源有二：provider 上记录的初值（连接诊断
// 实测写入），以及运行期上游的明确拒绝——拒绝时改写对应字段重发一次，结论在
// 生成器生命周期内保留，后续请求直接按新结论发送。不依赖任何模型名单。
type requestCompat struct {
	completionTokens atomic.Bool // 输出上限用 max_completion_tokens（否则 max_tokens）
	omitTemperature  atomic.Bool // 上游不接受自定义 temperature，省略该字段
}

func newRequestCompat(limit OutputLimitParam) *requestCompat {
	c := &requestCompat{}
	c.completionTokens.Store(limit == OutputLimitMaxCompletionTokens)
	return c
}

// outputLimitParam 返回当前生效的输出上限字段。
func (c *requestCompat) outputLimitParam() OutputLimitParam {
	if c.completionTokens.Load() {
		return OutputLimitMaxCompletionTokens
	}
	return OutputLimitMaxTokens
}

// apply 按当前结论写入输出上限与温度（limit<=0 不写上限；temperature nil 不写温度）。
func (c *requestCompat) apply(req *openai.ChatCompletionRequest, limit int, temperature *float64) {
	req.MaxTokens, req.MaxCompletionTokens = 0, 0
	if limit > 0 {
		if c.completionTokens.Load() {
			req.MaxCompletionTokens = limit
		} else {
			req.MaxTokens = limit
		}
	}
	req.Temperature = 0
	if temperature != nil && !c.omitTemperature.Load() {
		req.Temperature = float32(*temperature)
	}
}

// adapt 按上游对请求字段的明确拒绝调整结论；返回 true 表示本请求发出的正是被拒字段、
// 按当前结论改写后值得重发。只认参数级拒绝（400 且错误文本点名了该字段，或 SDK 的本地
// 同类校验），其余错误原样交给重试/降级层。
//
// 是否重发只看 sent（本请求实际发出的字段），不看本 goroutine 是否率先改了共享结论：
// 并发请求同带旧字段被拒时，后到的一方发现结论已被别人改好，同样应按新结论重发。
func (c *requestCompat) adapt(err error, sent *openai.ChatCompletionRequest) bool {
	switch rejectedParam(err) {
	case "max_tokens":
		if sent.MaxTokens > 0 {
			c.completionTokens.Store(true)
			return true
		}
	case "max_completion_tokens":
		if sent.MaxCompletionTokens > 0 {
			c.completionTokens.Store(false)
			return true
		}
	case "temperature":
		if sent.Temperature != 0 {
			c.omitTemperature.Store(true)
			return true
		}
	}
	return false
}

// rejectedParam 识别上游拒绝的请求字段名；不是参数级拒绝时返回空串。
func rejectedParam(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, openai.ErrReasoningModelMaxTokensDeprecated) {
		return "max_tokens"
	}
	if errors.Is(err, openai.ErrReasoningModelLimitationsOther) {
		return "temperature"
	}
	if statusOf(err) != http.StatusBadRequest {
		return ""
	}
	msg := strings.ToLower(err.Error())
	rejects := strings.Contains(msg, "unsupported") || strings.Contains(msg, "not supported") ||
		strings.Contains(msg, "does not support") || strings.Contains(msg, "only the default") ||
		strings.Contains(msg, "unknown parameter") || strings.Contains(msg, "unrecognized")
	if !rejects {
		return ""
	}
	switch {
	case strings.Contains(msg, "'max_tokens'") || strings.Contains(msg, "\"max_tokens\"") ||
		(strings.Contains(msg, "max_tokens") && strings.Contains(msg, "max_completion_tokens") && strings.Contains(msg, "use")):
		return "max_tokens"
	case strings.Contains(msg, "max_completion_tokens"):
		return "max_completion_tokens"
	case strings.Contains(msg, "temperature"):
		return "temperature"
	}
	return ""
}

// statusOf 取 OpenAI 兼容 SDK 错误携带的 HTTP 状态码（非 HTTP 错误为 0）。
func statusOf(err error) int {
	var he *HTTPError
	if errors.As(err, &he) {
		return he.Code
	}
	var ae *openai.APIError
	if errors.As(err, &ae) {
		return ae.HTTPStatusCode
	}
	var re *openai.RequestError
	if errors.As(err, &re) {
		return re.HTTPStatusCode
	}
	return 0
}
