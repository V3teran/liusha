package llm

import (
	"fmt"
	"sync"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	openai "github.com/sashabaranov/go-openai"
)

// ClientPool 单例化底层 HTTP client（OpenAI compat / Anthropic），
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
	opts := []option.RequestOption{option.WithAPIKey(apiKey)}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	c := anthropic.NewClient(opts...)
	p.anthropic[key] = &c
	return &c, nil
}
