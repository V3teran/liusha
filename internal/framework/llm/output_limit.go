package llm

import (
	"errors"
	"net/http"
	"strings"
)

// MinOutputLimit 是输出上限自动下调的下限：降到它以下仍被上游拒绝，就不是「上限过大」能解决
// 的问题了，错误原样交还。
const MinOutputLimit = 1024

// OutputLimit 是一个账户（服务地址 + 密钥）上一个模型的输出上限：从 provider 配置的值起，上游嫌它
// 过大而拒绝时降一半重发，并把结论记进 ClientPool。
//
// 记在池里而不是生成器实例上：Router 会定期重建 Provider 实例，记在实例上的结论每次重建都要
// 重新踩一遍。池是进程内的——api 与 runner 各记各的，进程重启后重新学。
type OutputLimit struct {
	pool       *ClientPool
	account    string
	model      string
	configured int
}

// OutputLimitFor 取这个账户上这个模型的输出上限，configured 是 provider 配置的单次最大输出。
func (p *ClientPool) OutputLimitFor(baseURL, apiKey, model string, configured int) *OutputLimit {
	return &OutputLimit{pool: p, account: baseURL + "|" + apiKey, model: model, configured: configured}
}

// forCall 是这次调用该请求的上限：请求级预算（收尾放大、探测省额度）给了就按它，否则按配置的值；
// 记住的较小上限封顶。
func (l *OutputLimit) forCall(requested int) int {
	limit := l.configured
	if requested > 0 {
		limit = requested
	}
	if cap := l.pool.outputLimitCap(l.account, l.model); cap > 0 && cap < limit {
		return cap
	}
	return limit
}

// rejected 在上游拒绝了 sent 这个上限之后给出下次该请求的值；不能再降返回 false。
func (l *OutputLimit) rejected(err error, sent int) (int, bool) {
	if !OutputLimitRejected(err) {
		return 0, false
	}
	next := l.pool.capOutputLimit(l.account, l.model, sent)
	return next, next > 0
}

// OutputLimitRejected 判定上游是否因为输出上限过大而拒绝了这次请求：400 且错误点名了输出上限
// 字段。各家的说法不一（must be at most、less than or equal to、greater than the maximum……），只认
// 字段名——它是我们自己送上去的，不会与别的拒绝理由混。
func OutputLimitRejected(err error) bool {
	var he *HTTPError
	if !errors.As(err, &he) || he.Code != http.StatusBadRequest {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, field := range [...]string{"max_tokens", "max_completion_tokens", "max_output_tokens"} {
		if strings.Contains(msg, field) {
			return true
		}
	}
	return false
}

func outputLimitKey(account, model string) string { return account + "|" + model }

// outputLimitCap 是记住的上限：这个账户上这个模型被上游接受过的最大值（0 = 没被拒绝过）。
func (p *ClientPool) outputLimitCap(account, model string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.outputLimits[outputLimitKey(account, model)]
}

// capOutputLimit 把记住的上限降到 rejected 的一半（不低于 MinOutputLimit）并返回它；降不动返回 0。
// 只降不升：几次并发调用各自被拒时取其中最小的。
func (p *ClientPool) capOutputLimit(account, model string, rejected int) int {
	next := rejected / 2
	if next < MinOutputLimit {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.outputLimits == nil {
		p.outputLimits = make(map[string]int)
	}
	key := outputLimitKey(account, model)
	if known := p.outputLimits[key]; known == 0 || next < known {
		p.outputLimits[key] = next
	}
	return next
}
