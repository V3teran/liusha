// Package httpapi: provider 实连探测 handler（测试连接 / 模型列表拉取）。
//
// 两端点服务前端「LLM 配置」页的即时反馈，避免「保存 → 建 run → 跑挂才知道钥错」的长反馈链：
//   - POST /models/providers/test        ：发一条最小 chat completion 验证 base_url+key+model 三者全对
//   - POST /models/providers/list-models ：GET {base_url}/models 拉可用模型名，前端做 datalist 探测
//
// 密钥来源双路径：请求体带 api_key（新建/更换密钥的明文，优先）→ 直接用；否则据 key 取已存
// provider——经 ProviderTester 适配器走 llmstore 多级缓存（内存 L1 / redis L2 / DB）读并解密，
// 不新开直连 DB 的读路径（与运行期 For(role) 同一套缓存事实源）。
package httpapi

import (
	"context"

	"github.com/gin-gonic/gin"

	"github.com/V3teran/liusha/internal/config/llmcfg"
)

// ProviderProbeSpec 是测试连接 / 模型探测的入参规格。
// APIKey 非空 → 用它（前端直填明文，新建或更换密钥场景）；为空 → 据 Key 取已存 provider 的密钥。
type ProviderProbeSpec struct {
	Key          string // 已存 provider key；APIKey 为空时据此走多级缓存取已存密钥并解密
	Type         string
	BaseURL      string
	DefaultModel string
	APIKey       string // 前端直填明文（优先于已存密钥）
}

// ProviderProbeResult 是测试连接结果。OK=false 时 ErrMsg 给中文原因（区分 401/404/超时/网络）。
type ProviderProbeResult struct {
	OK        bool
	LatencyMS int64
	Model     string // 实际用于测试的 model 名
	ErrMsg    string // 连接失败的中文原因（OK=false 时非空）
}

// ProviderTester 对一个 provider 规格做实连验证 / 拉取可用模型列表。
// cmd/api 注入闭合 llmstore（取已存密钥，多级缓存）+ cryptx（解密）+ internal/llm（建 client 发请求）的适配器。
// 为 nil 时两端点不注册（见 server.go）。
type ProviderTester interface {
	// TestProvider 发一条最小 chat completion。连接失败（401/404/超时/网络）体现在 Result.OK/ErrMsg，
	// 只有无法发起（解析已存密钥失败等）才返回 error。
	TestProvider(ctx context.Context, spec ProviderProbeSpec) (ProviderProbeResult, error)
	// ListModels 拉取 provider 可用模型名列表；provider 不支持 /models 时返回空切片（前端回退手填）。
	ListModels(ctx context.Context, spec ProviderProbeSpec) ([]string, error)
}

// probeBody 是 test / list-models 的公共请求体。
// key：编辑已存 provider 时传，据此取已存密钥（不必重填）；api_key：新建/更换密钥时传明文。
type probeBody struct {
	Key          string `json:"key"`
	Type         string `json:"type"`
	BaseURL      string `json:"base_url"`
	DefaultModel string `json:"default_model"`
	APIKey       string `json:"api_key"`
}

// specFromBody 组装 ProviderProbeSpec；校验「至少能定位一把钥」（明文或已存 key 二选一）。
func specFromBody(b probeBody) (ProviderProbeSpec, string) {
	if b.BaseURL == "" {
		return ProviderProbeSpec{}, "base_url 不能为空"
	}
	if b.APIKey == "" && b.Key == "" {
		return ProviderProbeSpec{}, "缺少密钥来源：请填入 api_key，或提供已存 provider 的 key"
	}
	return ProviderProbeSpec{
		Key: b.Key, Type: b.Type, BaseURL: b.BaseURL, DefaultModel: b.DefaultModel, APIKey: b.APIKey,
	}, ""
}

// testProviderHandler 处理 POST /models/providers/test。
// 连接失败不是 HTTP 错——统一 200 回 {ok:false, err_msg}，前端据此原地回显，无需区分 fetch reject。
func testProviderHandler(t ProviderTester) gin.HandlerFunc {
	return func(c *gin.Context) {
		var b probeBody
		if err := c.ShouldBindJSON(&b); err != nil {
			c.JSON(400, gin.H{"error": "请求体非法: " + err.Error()})
			return
		}
		if b.Type != "" && b.Type != llmcfg.ProviderTypeOpenAICompat && b.Type != llmcfg.ProviderTypeAnthropic {
			c.JSON(400, gin.H{"error": "非法 type（应为 openai_compat|anthropic）"})
			return
		}
		if b.DefaultModel == "" {
			c.JSON(400, gin.H{"error": "default_model 不能为空（测试连接需指定 model）"})
			return
		}
		spec, msg := specFromBody(b)
		if msg != "" {
			c.JSON(400, gin.H{"error": msg})
			return
		}
		res, err := t.TestProvider(c.Request.Context(), spec)
		if err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{
			"ok": res.OK, "latency_ms": res.LatencyMS, "model": res.Model, "err_msg": res.ErrMsg,
		})
	}
}

// listProviderModelsHandler 处理 POST /models/providers/list-models。
// 拉不到（provider 不支持 /models、或临时失败）返回空 models 列表，前端回退纯手填，不报错阻塞。
func listProviderModelsHandler(t ProviderTester) gin.HandlerFunc {
	return func(c *gin.Context) {
		var b probeBody
		if err := c.ShouldBindJSON(&b); err != nil {
			c.JSON(400, gin.H{"error": "请求体非法: " + err.Error()})
			return
		}
		spec, msg := specFromBody(b)
		if msg != "" {
			c.JSON(400, gin.H{"error": msg})
			return
		}
		models, err := t.ListModels(c.Request.Context(), spec)
		if err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		if models == nil {
			models = []string{}
		}
		c.JSON(200, gin.H{"models": models})
	}
}
