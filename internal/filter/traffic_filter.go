package filter

import (
	"net/http"

	"github.com/V3teran/liusha/internal/config"
)

// TrafficFilter 责任链门面：构造时一次性根据 ProxyConfig 装好链；运行时不可变。
//
// 与 liusha2 不同：本项目无 RuntimeConfig 热更新；所有配置静态，链构建一次即复用，
// 避免每条流量重建链造成的开销与不一致风险。
type TrafficFilter struct {
	chain *Chain
}

// NewTrafficFilter 按给定 ProxyConfig 一次性组装责任链。
//
//	链顺序（任一拒绝即终止）：
//	  Method → Protocol → Host(白+黑) → Suffix → ContentType → StatusCode → Size
func NewTrafficFilter(cfg config.ProxyConfig) *TrafficFilter {
	chain := NewChain()
	if len(cfg.ExcludeMethods) > 0 {
		chain.Add(NewMethodFilter(cfg.ExcludeMethods))
	}
	// 协议升级黑名单：未配置时兜底拦 websocket（绝大多数代理场景下不应进入扫描流量）。
	upgradeProtocols := cfg.ExcludeUpgradeProtocols
	if len(upgradeProtocols) == 0 {
		upgradeProtocols = []string{"websocket"}
	}
	chain.Add(NewProtocolFilter(upgradeProtocols))
	chain.Add(NewHostFilter(cfg.AllowHosts, cfg.ExcludeHosts))
	if len(cfg.ExcludeSuffixes) > 0 {
		chain.Add(NewSuffixFilter(cfg.ExcludeSuffixes))
	}
	if len(cfg.ExcludeContentTypes) > 0 {
		chain.Add(NewContentTypeFilter(cfg.ExcludeContentTypes))
	}
	if len(cfg.ExcludeStatusCodes) > 0 {
		chain.Add(NewStatusCodeFilter(cfg.ExcludeStatusCodes))
	}
	chain.Add(NewSizeFilter(cfg.MaxRequestBodySize, cfg.MaxResponseBodySize))

	return &TrafficFilter{chain: chain}
}

// ShouldProcess 统一入口：true=保留，false+reason=丢弃。
func (f *TrafficFilter) ShouldProcess(req *http.Request, resp *http.Response) (bool, string) {
	return f.chain.ShouldProcess(req, resp)
}
