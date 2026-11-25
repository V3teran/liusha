package llmstore

import (
	"time"

	"github.com/V3teran/liusha/internal/config"
	fwllm "github.com/V3teran/liusha/internal/framework/llm"
)

// RetryOptions 把部署配置里的重试参数（次数、以秒书写的退避表）换算为框架的重试参数。
// api 与 runner 装配 Router 共用，所以放在两者都依赖的这一层。
//
// 不放在 config 包：config 被每个进程引用，包括多镜像部署的沙盒容器——那里的程序只该依赖
// 沙盒自己的东西，不该为了一个换算背上模型框架（沙盒服务端的依赖面由 internal/app/sandbox
// 的测试守住）。
func RetryOptions(c config.RetryConfig) fwllm.RetryOptions {
	seconds := func(secs []int) []time.Duration {
		out := make([]time.Duration, len(secs))
		for i, s := range secs {
			out[i] = time.Duration(s) * time.Second
		}
		return out
	}
	return fwllm.RetryOptions{
		MaxRetries429: c.Max429,
		MaxRetries529: c.Max529,
		MaxRetries5xx: c.Max5xx,
		MaxRetriesNet: c.MaxNet,
		Backoff429:    seconds(c.Backoff429Seconds),
		Backoff5xx:    seconds(c.Backoff5xxSeconds),
		BackoffNet:    seconds(c.BackoffNetSeconds),
		Backoff529:    time.Duration(c.Backoff529Ms) * time.Millisecond,
	}
}
