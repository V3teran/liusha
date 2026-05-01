// Package filter 提供 HTTP 流量责任链过滤器。
// 静态构造：所有参数构造时传入，运行时不变（无 RuntimeConfig 热更新）。
package filter

import (
	"fmt"
	"net/http"
	"strings"
)

// Filter 单个过滤器接口。返回 (true, "") 放行；返回 (false, reason) 拦截并附原因。
type Filter interface {
	ShouldProcess(req *http.Request, resp *http.Response) (bool, string)
}

// Chain 责任链：任一 Filter 拒绝即整体拒绝。
type Chain struct {
	filters []Filter
}

// NewChain 构造一条责任链。
func NewChain(filters ...Filter) *Chain {
	return &Chain{filters: filters}
}

// Add 追加一个 Filter（链式调用风格）。
func (c *Chain) Add(f Filter) *Chain {
	c.filters = append(c.filters, f)
	return c
}

// ShouldProcess 顺序执行链上每个 Filter；任意拒绝即返回 false + reason。
func (c *Chain) ShouldProcess(req *http.Request, resp *http.Response) (bool, string) {
	for _, f := range c.filters {
		if ok, reason := f.ShouldProcess(req, resp); !ok {
			return false, reason
		}
	}
	return true, ""
}

// MethodFilter HTTP 方法黑名单（OPTIONS/HEAD/CONNECT 等）。
type MethodFilter struct {
	excludeMethods map[string]struct{}
}

// NewMethodFilter 创建方法过滤器；methods 大小写不敏感。
func NewMethodFilter(methods []string) *MethodFilter {
	m := make(map[string]struct{}, len(methods))
	for _, method := range methods {
		m[strings.ToUpper(method)] = struct{}{}
	}
	return &MethodFilter{excludeMethods: m}
}

func (f *MethodFilter) ShouldProcess(req *http.Request, _ *http.Response) (bool, string) {
	if _, hit := f.excludeMethods[strings.ToUpper(req.Method)]; hit {
		return false, "excluded method: " + req.Method
	}
	return true, ""
}

// ProtocolFilter 协议升级过滤器（拦 WebSocket 等 Upgrade 请求与 101 响应）。
type ProtocolFilter struct {
	excludeProtocols map[string]struct{}
}

// NewProtocolFilter 默认外部应传 ["websocket"]。
func NewProtocolFilter(protocols []string) *ProtocolFilter {
	m := make(map[string]struct{}, len(protocols))
	for _, p := range protocols {
		m[strings.ToLower(p)] = struct{}{}
	}
	return &ProtocolFilter{excludeProtocols: m}
}

func (f *ProtocolFilter) ShouldProcess(req *http.Request, resp *http.Response) (bool, string) {
	upgrade := strings.ToLower(req.Header.Get("Upgrade"))
	if upgrade != "" {
		if _, hit := f.excludeProtocols[upgrade]; hit {
			return false, "excluded upgrade protocol: " + upgrade
		}
	}
	if resp != nil && resp.StatusCode == http.StatusSwitchingProtocols {
		return false, "protocol switching response (101)"
	}
	return true, ""
}

// HostFilter Host 白名单 + 黑名单。
//
// 语义：
//   - 黑名单优先：host 命中 excludeHosts 立即拒。
//   - 白名单空=放行全部（仅黑名单生效）。
//   - 白名单非空：host 必须命中 includeHosts 才放行，否则拒。
//   - 两个名单都支持前缀通配 *.example.com（即 example.com 自身 + 任意子域）。
type HostFilter struct {
	includePatterns []string
	excludePatterns []string
}

func NewHostFilter(includeHosts, excludeHosts []string) *HostFilter {
	return &HostFilter{
		includePatterns: includeHosts,
		excludePatterns: excludeHosts,
	}
}

func (f *HostFilter) ShouldProcess(req *http.Request, _ *http.Response) (bool, string) {
	host := req.Host
	if host == "" && req.URL != nil {
		host = req.URL.Host
	}
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}

	for _, pattern := range f.excludePatterns {
		if matchHostGlob(host, pattern) {
			return false, "excluded host: " + host
		}
	}

	if len(f.includePatterns) > 0 {
		for _, pattern := range f.includePatterns {
			if matchHostGlob(host, pattern) {
				return true, ""
			}
		}
		return false, "host not in whitelist: " + host
	}
	return true, ""
}

// matchHostGlob 仅支持前缀通配 *.xxx.com，覆盖最常见用例；其余按等值匹配。
func matchHostGlob(host, pattern string) bool {
	if !strings.Contains(pattern, "*") {
		return host == pattern
	}
	if strings.HasPrefix(pattern, "*.") {
		suffix := pattern[1:]
		return strings.HasSuffix(host, suffix) || host == pattern[2:]
	}
	return false
}

// SuffixFilter URL Path 后缀黑名单（.css/.js/图片字体等）。
type SuffixFilter struct {
	excludeSuffixes []string
}

func NewSuffixFilter(excludeSuffixes []string) *SuffixFilter {
	return &SuffixFilter{excludeSuffixes: excludeSuffixes}
}

func (f *SuffixFilter) ShouldProcess(req *http.Request, _ *http.Response) (bool, string) {
	path := ""
	if req.URL != nil {
		path = req.URL.Path
	}
	pathLower := strings.ToLower(path)
	for _, suffix := range f.excludeSuffixes {
		if strings.HasSuffix(pathLower, strings.ToLower(suffix)) {
			return false, "excluded suffix: " + suffix
		}
	}
	return true, ""
}

// ContentTypeFilter 响应 Content-Type 黑名单；支持 image/* 之类前缀通配。
type ContentTypeFilter struct {
	excludeTypes []string
}

func NewContentTypeFilter(excludeTypes []string) *ContentTypeFilter {
	return &ContentTypeFilter{excludeTypes: excludeTypes}
}

func (f *ContentTypeFilter) ShouldProcess(_ *http.Request, resp *http.Response) (bool, string) {
	if resp == nil {
		return true, ""
	}
	contentType := resp.Header.Get("Content-Type")
	for _, pattern := range f.excludeTypes {
		if matchContentType(contentType, pattern) {
			return false, "excluded content-type: " + contentType
		}
	}
	return true, ""
}

// matchContentType 支持 "image/*" 前缀匹配与精确匹配（忽略 charset 等参数）。
// 大小写不敏感（HTTP 头允许 mixed case，例如 `Image/PNG`、`Application/JSON`）。
func matchContentType(contentType, pattern string) bool {
	contentType = strings.ToLower(contentType)
	pattern = strings.ToLower(pattern)
	if strings.HasSuffix(pattern, "/*") {
		prefix := strings.TrimSuffix(pattern, "/*")
		return strings.HasPrefix(contentType, prefix+"/") || strings.HasPrefix(contentType, prefix+";")
	}
	semi := strings.IndexByte(contentType, ';')
	base := contentType
	if semi >= 0 {
		base = strings.TrimSpace(contentType[:semi])
	}
	return base == pattern
}

// StatusCodeFilter 状态码黑名单：命中 excludeCodes 即拒；空集合=放行全部。
type StatusCodeFilter struct {
	excludeCodes map[int]struct{}
}

// NewStatusCodeFilter 创建状态码黑名单过滤器。
// excludeCodes 为空时该过滤器对全部响应放行。
func NewStatusCodeFilter(excludeCodes []int) *StatusCodeFilter {
	m := make(map[int]struct{}, len(excludeCodes))
	for _, c := range excludeCodes {
		m[c] = struct{}{}
	}
	return &StatusCodeFilter{excludeCodes: m}
}

func (f *StatusCodeFilter) ShouldProcess(_ *http.Request, resp *http.Response) (bool, string) {
	if len(f.excludeCodes) == 0 || resp == nil {
		return true, ""
	}
	if _, hit := f.excludeCodes[resp.StatusCode]; hit {
		return false, fmt.Sprintf("excluded status code: %d", resp.StatusCode)
	}
	return true, ""
}

// SizeFilter 请求体/响应体大小上限（0=不限）。
type SizeFilter struct {
	maxRequestBodySize  int
	maxResponseBodySize int
}

func NewSizeFilter(maxRequestBodySize, maxResponseBodySize int) *SizeFilter {
	return &SizeFilter{
		maxRequestBodySize:  maxRequestBodySize,
		maxResponseBodySize: maxResponseBodySize,
	}
}

func (f *SizeFilter) ShouldProcess(req *http.Request, resp *http.Response) (bool, string) {
	if f.maxRequestBodySize > 0 && req.ContentLength > 0 && int(req.ContentLength) > f.maxRequestBodySize {
		return false, fmt.Sprintf("request body too large: %d > %d", req.ContentLength, f.maxRequestBodySize)
	}
	if f.maxResponseBodySize > 0 && resp != nil && resp.ContentLength > 0 && int(resp.ContentLength) > f.maxResponseBodySize {
		return false, fmt.Sprintf("response body too large: %d > %d", resp.ContentLength, f.maxResponseBodySize)
	}
	return true, ""
}
