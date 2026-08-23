// Package main: provider 实连探测适配器，满足 httpapi.ProviderTester 窄接口。
//
// 装配 llmstore（取已存密钥，走内存/redis/DB 多级缓存）+ cryptx（解密）+ internal/llm
// （建 client 发请求）。测试连接发一条最小 chat completion 验证 base_url+key+model；
// 模型探测 GET {base_url}/models 拉可用模型名。
//
// 密钥双路径与运行期一致：请求带明文 key（新建/更换密钥）优先直用；否则据 key 从 llmstore
// 取已存 provider 并解密——同 For(role) 一套缓存事实源，不新开直连 DB 读。
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/V3teran/liusha/internal/config/llmcfg"
	"github.com/V3teran/liusha/internal/httpapi"
	"github.com/V3teran/liusha/internal/llm"
)

// providerStore 是探测适配器对 llmstore 的窄依赖：据 key 取已存 provider（多级缓存）。
type providerStore interface {
	ProviderByKey(ctx context.Context, key string) (llmcfg.Provider, error)
}

// providerTester 实现 httpapi.ProviderTester。
type providerTester struct {
	store providerStore
	dec   llmcfg.KeyDecrypter // 解密已存 provider 的密文密钥
	pool  *llm.ClientPool     // 复用共享 HTTP client（与运行期同池）
	http  *http.Client        // list-models 的裸 HTTP 探测用（带超时）
}

// newProviderTester 装配探测适配器。dec/store/pool 缺任一则不应注册路由（cmd/api 负责判空）。
func newProviderTester(store providerStore, dec llmcfg.KeyDecrypter, pool *llm.ClientPool) *providerTester {
	return &providerTester{
		store: store,
		dec:   dec,
		pool:  pool,
		http:  &http.Client{Timeout: probeTimeout},
	}
}

// probeTimeout 是探测请求的整体超时——测试连接/模型拉取都是交互式即时反馈，不宜久等。
const probeTimeout = 15 * time.Second

// resolveKey 决定这次探测用哪把密钥：请求明文优先，否则据 key 取已存并解密。
func (t *providerTester) resolveKey(ctx context.Context, spec httpapi.ProviderProbeSpec) (string, error) {
	if spec.APIKey != "" {
		return spec.APIKey, nil // 前端直填明文（新建/更换密钥），直接用
	}
	stored, err := t.store.ProviderByKey(ctx, spec.Key)
	if err != nil {
		return "", fmt.Errorf("取已存 provider %q 失败: %w", spec.Key, err)
	}
	key, err := llmcfg.ResolveAPIKey(stored, t.dec) // 解密 or env 回退（旧数据路径）
	if err != nil {
		return "", err
	}
	return key, nil
}

// providerFromSpec 用表单当前值组装待测 provider，MaxTokens 强制 1（测试只需能通就行，省 token）。
func providerFromSpec(spec httpapi.ProviderProbeSpec) llmcfg.Provider {
	return llmcfg.Provider{
		Key:          spec.Key,
		Type:         spec.Type,
		BaseURL:      spec.BaseURL,
		DefaultModel: spec.DefaultModel,
		MaxTokens:    1,
	}
}

// TestProvider 发一条最小 chat completion。连接类失败（鉴权/找不到/超时/网络）落在 Result.ErrMsg，
// 只有无法发起（取密钥失败、建 client 失败）才返回 error。
func (t *providerTester) TestProvider(ctx context.Context, spec httpapi.ProviderProbeSpec) (httpapi.ProviderProbeResult, error) {
	key, err := t.resolveKey(ctx, spec)
	if err != nil {
		return httpapi.ProviderProbeResult{}, err
	}
	p := providerFromSpec(spec)
	gen, err := llm.BuildProviderWithKey(ctx, p, t.pool, key)
	if err != nil {
		return httpapi.ProviderProbeResult{}, fmt.Errorf("构造 client 失败: %w", err)
	}

	cctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	start := time.Now()
	_, genErr := gen.Generate(cctx, []llm.Message{{Role: llm.RoleUser, Content: "ping"}}, nil)
	latency := time.Since(start).Milliseconds()

	if genErr != nil {
		return httpapi.ProviderProbeResult{
			OK: false, LatencyMS: latency, Model: p.DefaultModel, ErrMsg: humanizeProbeErr(genErr),
		}, nil
	}
	return httpapi.ProviderProbeResult{OK: true, LatencyMS: latency, Model: p.DefaultModel}, nil
}

// humanizeProbeErr 把底层 error 归类成中文原因（鉴权/模型不存在/超时/网络/其他）。
// 内部 llm 层把状态码包进 err 字符串，这里做启发式匹配（与 retry.classify 同源思路）。
func humanizeProbeErr(err error) string {
	msg := err.Error()
	low := strings.ToLower(msg)
	switch {
	case strings.Contains(msg, "401") || strings.Contains(low, "unauthorized") || strings.Contains(low, "invalid api key"):
		return "鉴权失败：API Key 无效或无权限（401）"
	case strings.Contains(msg, "403") || strings.Contains(low, "forbidden"):
		return "拒绝访问：Key 无该操作权限（403）"
	case strings.Contains(msg, "404") || strings.Contains(low, "not found") || strings.Contains(low, "model_not_found"):
		return "找不到资源：base_url 路径或 model 名有误（404）"
	case strings.Contains(msg, "429") || strings.Contains(low, "rate limit"):
		return "触发限流：请求过于频繁或额度耗尽（429）"
	case strings.Contains(low, "timeout") || strings.Contains(low, "deadline exceeded") || strings.Contains(low, "i/o timeout"):
		return "连接超时：base_url 不可达或响应过慢"
	case strings.Contains(low, "connection refused") || strings.Contains(low, "connection reset") || strings.Contains(low, "no such host") || strings.Contains(low, "dial"):
		return "网络错误：无法连接到 base_url（域名解析/端口/证书）"
	default:
		return "连接失败：" + msg
	}
}

// ListModels 拉取 provider 可用模型名。OpenAI 兼容走 GET {base_url}/models（Bearer 鉴权），
// anthropic 走同路径但用 x-api-key + anthropic-version 头。provider 不支持时返回空切片（前端回退手填）。
func (t *providerTester) ListModels(ctx context.Context, spec httpapi.ProviderProbeSpec) ([]string, error) {
	key, err := t.resolveKey(ctx, spec)
	if err != nil {
		return nil, err
	}
	url := strings.TrimRight(spec.BaseURL, "/") + "/models"

	cctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("构造请求失败: %w", err)
	}
	if spec.Type == llmcfg.ProviderTypeAnthropic {
		req.Header.Set("x-api-key", key)
		req.Header.Set("anthropic-version", "2023-06-01")
	} else {
		req.Header.Set("Authorization", "Bearer "+key)
	}

	resp, err := t.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("拉取模型列表失败: %s", humanizeProbeErr(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("拉取模型列表失败（HTTP %d）：%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	// OpenAI / Anthropic 都用 {"data":[{"id":"..."}]} 形状。
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload); err != nil {
		return nil, fmt.Errorf("解析模型列表失败: %w", err)
	}
	models := make([]string, 0, len(payload.Data))
	for _, m := range payload.Data {
		if m.ID != "" {
			models = append(models, m.ID)
		}
	}
	return models, nil
}
