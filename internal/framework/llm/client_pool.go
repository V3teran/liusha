package llm

import (
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	openai "github.com/sashabaranov/go-openai"
)

// llmHTTPClient 是 LLM 出站连接的共享 HTTP client。
//
// 超时只布在 Transport 各阶段（拨号/TLS/响应头），不设 Client.Timeout——
// 流式响应可合法运行数分钟，整体超时会掐断活流。各阶段超时兜底的是
// 连接黑洞（对端不握手/不回包），生成耗时的控制归业务侧单调用看门狗。
func llmHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 120 * time.Second,
			IdleConnTimeout:       90 * time.Second,
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   10,
		},
	}
}

// credentialTransport 是出站凭据的唯一出口：清掉请求上所有凭据头（SDK 自带的、
// 以及 SDK 从环境变量 ANTHROPIC_API_KEY / ANTHROPIC_AUTH_TOKEN 自动注入的外来值），
// 再按 provider 记录的鉴权方式只写一个。两个 SDK 的鉴权行为因此完全一致且可预期——
// 出站请求不携带任何我们不控制的凭据（兼容网关普遍优先读 Authorization，外来
// 占位值会让真实密钥被无视）。
type credentialTransport struct {
	next   http.RoundTripper
	key    string
	scheme AuthScheme
}

// credentialHeaders 是会被清掉重写的凭据头。
var credentialHeaders = []string{"Authorization", "X-Api-Key", "Api-Key"}

func (t *credentialTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r := req.Clone(req.Context())
	for _, h := range credentialHeaders {
		r.Header.Del(h)
	}
	switch t.scheme {
	case AuthXAPIKey:
		r.Header.Set("X-Api-Key", t.key)
	default:
		r.Header.Set("Authorization", "Bearer "+t.key)
	}
	return t.next.RoundTrip(r)
}

// credentialedHTTPClient 返回按鉴权方式改写凭据头的共享 HTTP client。
func credentialedHTTPClient(key string, scheme AuthScheme) *http.Client {
	c := llmHTTPClient()
	c.Transport = &credentialTransport{next: c.Transport, key: key, scheme: scheme}
	return c
}

// ClientPool 按 (baseURL, key, 鉴权方式) 单例化底层 HTTP client（OpenAI compat / Anthropic），
// 让 Generator 跨 task 共享 http 连接池，修复 v1 Factory 缓存 Generator
// 导致跨 task tools 错乱的并发 bug。
type ClientPool struct {
	mu        sync.Mutex
	openai    map[string]*openai.Client
	anthropic map[string]*anthropic.Client
	transport []*http.Client // 池内创建的 HTTP client（Close 释放其空闲连接）
}

// NewClientPool 构造空池。
func NewClientPool() *ClientPool {
	return &ClientPool{
		openai:    make(map[string]*openai.Client),
		anthropic: make(map[string]*anthropic.Client),
	}
}

// poolKey 是 client 的复用键。scheme 为空时按协议标准方式归一，避免同一配置两份 client。
func poolKey(baseURL, apiKey string, scheme AuthScheme) string {
	return baseURL + "|" + string(scheme) + "|" + apiKey
}

// GetOrCreateOpenAI 按 baseURL+apiKey+鉴权方式共享 OpenAI 兼容 client。
// baseURL 空字符串走 OpenAI 官方默认；scheme 空 = Bearer（协议标准）。
func (p *ClientPool) GetOrCreateOpenAI(baseURL, apiKey string, scheme AuthScheme) (*openai.Client, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("ClientPool: openai apikey 必填")
	}
	if scheme == "" {
		scheme = DefaultAuthScheme(ProviderTypeOpenAICompat)
	}
	key := poolKey(baseURL, apiKey, scheme)
	p.mu.Lock()
	defer p.mu.Unlock()
	if c, ok := p.openai[key]; ok {
		return c, nil
	}
	cfg := openai.DefaultConfig(apiKey)
	if baseURL != "" {
		cfg.BaseURL = baseURL
	}
	cfg.HTTPClient = p.newHTTPClient(apiKey, scheme)
	c := openai.NewClientWithConfig(cfg)
	p.openai[key] = c
	return c, nil
}

// GetOrCreateAnthropic 按 baseURL+apiKey+鉴权方式共享 Anthropic client。
// scheme 空 = x-api-key（协议标准）。
func (p *ClientPool) GetOrCreateAnthropic(baseURL, apiKey string, scheme AuthScheme) (*anthropic.Client, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("ClientPool: anthropic apikey 必填")
	}
	if scheme == "" {
		scheme = DefaultAuthScheme(ProviderTypeAnthropic)
	}
	key := poolKey(baseURL, apiKey, scheme)
	p.mu.Lock()
	defer p.mu.Unlock()
	if c, ok := p.anthropic[key]; ok {
		return c, nil
	}
	opts := []option.RequestOption{
		option.WithAPIKey(apiKey), // SDK 前置校验需要；实际凭据头由 credentialTransport 统一改写
		option.WithHTTPClient(p.newHTTPClient(apiKey, scheme)),
	}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	c := anthropic.NewClient(opts...)
	p.anthropic[key] = &c
	return &c, nil
}

// newHTTPClient 创建按鉴权方式改写凭据头的 HTTP client 并登记（调用方持锁）。
func (p *ClientPool) newHTTPClient(apiKey string, scheme AuthScheme) *http.Client {
	c := credentialedHTTPClient(apiKey, scheme)
	p.transport = append(p.transport, c)
	return c
}

// Close 释放池内全部 client 并关闭其空闲连接。用于生命周期有界的池（如单次连接诊断）：
// 池里的 client 以明文密钥为复用键，用完即弃，不让临时输入的凭据驻留在进程里。
func (p *ClientPool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.transport {
		c.CloseIdleConnections()
	}
	p.openai = make(map[string]*openai.Client)
	p.anthropic = make(map[string]*anthropic.Client)
	p.transport = nil
}
