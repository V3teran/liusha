// Package scope 实现渗透测试的 URL 作用域策略，供代理层、Agent 层、工具层三处统一调用。
package scope

import (
	"fmt"
	"net/url"
	"strings"
)

// Rules 是从 engagement.Scope JSON 反序列化的结构化策略。
type Rules struct {
	Domains  []string `json:"domains"`
	Excludes []string `json:"excludes"`
	Methods  []string `json:"methods"`
}

// ScopeViolation 描述一条请求超出 scope 的原因。
type ScopeViolation struct {
	Reason string // domain_not_allowed / method_not_allowed / path_excluded / invalid_url
	Detail string
}

// Policy 是线程安全的只读策略对象，由 NewPolicy 构造后不可变。
type Policy struct {
	domains  []string
	excludes []string
	methods  map[string]struct{} // 空 map 表示允许所有方法
}

// NewPolicy 解析 Rules 并预处理为内部形式；domains 不能为空。
func NewPolicy(r Rules) (*Policy, error) {
	if len(r.Domains) == 0 {
		return nil, fmt.Errorf("scope: domains 不能为空")
	}
	methods := make(map[string]struct{}, len(r.Methods))
	for _, m := range r.Methods {
		methods[strings.ToUpper(m)] = struct{}{}
	}
	return &Policy{
		domains:  r.Domains,
		excludes: r.Excludes,
		methods:  methods,
	}, nil
}

// InScope 返回请求是否在策略允许的范围内。
func (p *Policy) InScope(method, rawURL string) bool {
	return p.Violation(method, rawURL) == nil
}

// Violation 返回第一个违规原因；nil 表示 in-scope。
func (p *Policy) Violation(method, rawURL string) *ScopeViolation {
	u, err := url.Parse(rawURL)
	if err != nil {
		return &ScopeViolation{Reason: "invalid_url", Detail: err.Error()}
	}

	if !p.domainAllowed(u.Hostname()) {
		return &ScopeViolation{Reason: "domain_not_allowed", Detail: u.Hostname()}
	}

	if len(p.methods) > 0 {
		if _, ok := p.methods[strings.ToUpper(method)]; !ok {
			return &ScopeViolation{Reason: "method_not_allowed", Detail: method}
		}
	}

	if p.pathExcluded(u.Path) {
		return &ScopeViolation{Reason: "path_excluded", Detail: u.Path}
	}

	return nil
}

// domainAllowed 检查 host 是否匹配白名单。
// *.foo.com 只匹配一级子域（不递归，不匹配 foo.com 本身）。
func (p *Policy) domainAllowed(host string) bool {
	for _, d := range p.domains {
		if strings.HasPrefix(d, "*.") {
			suffix := d[1:] // ".example.com"
			if strings.HasSuffix(host, suffix) {
				prefix := host[:len(host)-len(suffix)]
				if prefix != "" && !strings.Contains(prefix, ".") {
					return true
				}
			}
		} else if host == d {
			return true
		}
	}
	return false
}

// pathExcluded 检查路径是否命中排除规则。
// 规则以 /* 结尾时匹配前缀；否则精确匹配。
func (p *Policy) pathExcluded(path string) bool {
	for _, ex := range p.excludes {
		if strings.HasSuffix(ex, "/*") {
			prefix := ex[:len(ex)-2]
			if strings.HasPrefix(path, prefix+"/") || path == prefix {
				return true
			}
		} else if path == ex {
			return true
		}
	}
	return false
}
