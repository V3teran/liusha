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

// ClientPool 按 (baseURL, key) 单例化底层 HTTP client（OpenAI compat / Anthropic），
// 让 Generator 跨 task 共享 http 连接池，修复 v1 Factory 缓存 Generator
// 导致跨 task tools 错乱的并发 bug。
type ClientPool struct {
	mu        sync.Mutex
	openai    map[string]*openai.Client
	anthropic map[string]*anthropic.Client
}

// NewClientPool 构造空池。
func NewClientPool() *ClientPool {
	return &ClientPool{
		openai:    make(map[string]*openai.Client),
		anthropic: make(map[string]*anthropic.Client),
	}
}

// GetOrCreateOpenAI 按 baseURL+apiKey 共享 client。
//
// baseURL 空字符串走 OpenAI 官方默认。
func (p *ClientPool) GetOrCreateOpenAI(baseURL, apiKey string) (*openai.Client, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("ClientPool: openai apikey 必填")
	}
	key := baseURL + "|" + apiKey
	p.mu.Lock()
	defer p.mu.Unlock()
	if c, ok := p.openai[key]; ok {
		return c, nil
	}
	cfg := openai.DefaultConfig(apiKey)
	if baseURL != "" {
		cfg.BaseURL = baseURL
	}
	cfg.HTTPClient = llmHTTPClient()
	c := openai.NewClientWithConfig(cfg)
	p.openai[key] = c
	return c, nil
}

// GetOrCreateAnthropic 按 baseURL+apiKey 共享 client。
func (p *ClientPool) GetOrCreateAnthropic(baseURL, apiKey string) (*anthropic.Client, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("ClientPool: anthropic apikey 必填")
	}
	key := baseURL + "|" + apiKey
	p.mu.Lock()
	defer p.mu.Unlock()
	if c, ok := p.anthropic[key]; ok {
		return c, nil
	}
	// scrubAuthHeader 擦除 Authorization 头。SDK 会自动读环境变量 ANTHROPIC_AUTH_TOKEN
	// 并附加 Authorization: Bearer <值>（本进程之外的调用方工具链常设此变量做自己的
	// LLM 路由）；而本系统的密钥统一走 x-api-key（DB/env 双路径解析），出站请求不允许
	// 携带我们不控制的凭据——兼容网关普遍优先读 Authorization，外来占位值会让真实的
	// key 被无视、报 401「令牌无效」。
	scrubAuthHeader := func(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
		req.Header.Del("Authorization")
		return next(req)
	}
	opts := []option.RequestOption{
		option.WithAPIKey(apiKey),
		option.WithHTTPClient(llmHTTPClient()),
		option.WithMiddleware(scrubAuthHeader),
	}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	c := anthropic.NewClient(opts...)
	p.anthropic[key] = &c
	return &c, nil
}
