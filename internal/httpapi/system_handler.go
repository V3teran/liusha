// Package httpapi: 系统配置 handler（前端「系统配置」页）。
//
// 三组业务旋钮对应 migration 0098 的 system_setting 分组 KV：
//   - compaction   ：会话历史压缩（触发阈值 / trailing 预算 / 蒸馏超时）
//   - runtime      ：工具运行时（单步工具超时 / 输出截尾 / prompt finding 上限）
//   - proxy_filter ：代理流量过滤规则（黑白名单 + body 上限）
//
// 写路径一律走 settingstore（落 DB + redis 广播失效）：runner 下次现读即拿到最新
// compaction/runtime 旋钮；proxy 进程订阅 proxy_filter 失效后热换过滤链（真热改，无需重启）。
// handler 只做 HTTP 编解码 + 校验，绝不直穿底层 store。
package httpapi

import (
	"context"

	"github.com/gin-gonic/gin"

	"github.com/V3teran/liusha/internal/config/settingstore"
)

// SettingsAPI 是系统配置 CRUD 依赖的窄接口；*settingstore.Store 自动满足。
// 读经多级缓存、写经失效广播的语义全在 settingstore 内。
type SettingsAPI interface {
	Compaction(ctx context.Context) (settingstore.CompactionSettings, error)
	SaveCompaction(ctx context.Context, v settingstore.CompactionSettings) error
	Runtime(ctx context.Context) (settingstore.RuntimeSettings, error)
	SaveRuntime(ctx context.Context, v settingstore.RuntimeSettings) error
	ProxyFilter(ctx context.Context) (settingstore.ProxyFilterSettings, error)
	SaveProxyFilter(ctx context.Context, v settingstore.ProxyFilterSettings) error
}

// ── compaction（会话历史压缩旋钮）────────────────────────────────────

// getCompactionSettingsHandler 处理 GET /settings/compaction。
func getCompactionSettingsHandler(api SettingsAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		v, err := api.Compaction(c.Request.Context())
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{"compaction": v})
	}
}

// putCompactionSettingsHandler 处理 PUT /settings/compaction（全量覆写 + 校验）。
func putCompactionSettingsHandler(api SettingsAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		var b settingstore.CompactionSettings
		if err := c.ShouldBindJSON(&b); err != nil {
			c.JSON(400, gin.H{"error": "请求体非法: " + err.Error()})
			return
		}
		// 比例是窗口占比，须落在 (0,1]；蒸馏超时须为正秒数。快速失败给清晰中文提示。
		if b.TriggerRatio <= 0 || b.TriggerRatio > 1 {
			c.JSON(400, gin.H{"error": "trigger_ratio 必须在 (0, 1] 区间（触发压缩的窗口占比）"})
			return
		}
		if b.TrailingBudgetRatio <= 0 || b.TrailingBudgetRatio > 1 {
			c.JSON(400, gin.H{"error": "trailing_budget_ratio 必须在 (0, 1] 区间（会话历史占窗口比例）"})
			return
		}
		if b.CompactorTimeoutSeconds <= 0 {
			c.JSON(400, gin.H{"error": "compactor_timeout_seconds 必须 > 0（旧会话蒸馏单次 LLM 超时秒数）"})
			return
		}
		if err := api.SaveCompaction(c.Request.Context(), b); err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{"compaction": b})
	}
}

// ── runtime（工具运行时旋钮）─────────────────────────────────────────

// getRuntimeSettingsHandler 处理 GET /settings/runtime。
func getRuntimeSettingsHandler(api SettingsAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		v, err := api.Runtime(c.Request.Context())
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{"runtime": v})
	}
}

// putRuntimeSettingsHandler 处理 PUT /settings/runtime（全量覆写 + 校验）。
func putRuntimeSettingsHandler(api SettingsAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		var b settingstore.RuntimeSettings
		if err := c.ShouldBindJSON(&b); err != nil {
			c.JSON(400, gin.H{"error": "请求体非法: " + err.Error()})
			return
		}
		if b.StepToolTimeoutSeconds <= 0 {
			c.JSON(400, gin.H{"error": "step_tool_timeout_seconds 必须 > 0（单步工具执行兜底超时秒数）"})
			return
		}
		if b.RunTailBytes <= 0 {
			c.JSON(400, gin.H{"error": "run_tail_bytes 必须 > 0（stdout/stderr 截尾字节数）"})
			return
		}
		if b.FindingsLimitInPrompt <= 0 {
			c.JSON(400, gin.H{"error": "findings_limit_in_prompt 必须 > 0（prompt 注入 finding 的 DB 读上限）"})
			return
		}
		if err := api.SaveRuntime(c.Request.Context(), b); err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{"runtime": b})
	}
}

// ── proxy_filter(代理流量过滤规则)────────────────────────────────────

// getProxyFilterSettingsHandler 处理 GET /settings/proxy-filter。
func getProxyFilterSettingsHandler(api SettingsAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		v, err := api.ProxyFilter(c.Request.Context())
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{"proxy_filter": v})
	}
}

// putProxyFilterSettingsHandler 处理 PUT /settings/proxy-filter（全量覆写 + 校验）。
// 保存后经失效总线广播，proxy 进程热换过滤链（下一条流量即走新规则）。
func putProxyFilterSettingsHandler(api SettingsAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		var b settingstore.ProxyFilterSettings
		if err := c.ShouldBindJSON(&b); err != nil {
			c.JSON(400, gin.H{"error": "请求体非法: " + err.Error()})
			return
		}
		// body 上限须为正（LimitReader 语义：0 会截成空 body，几乎必是误填）。
		if b.MaxRequestBodySize <= 0 {
			c.JSON(400, gin.H{"error": "max_request_body_size 必须 > 0（请求体切片上限字节）"})
			return
		}
		if b.MaxResponseBodySize <= 0 {
			c.JSON(400, gin.H{"error": "max_response_body_size 必须 > 0（响应体切片上限字节）"})
			return
		}
		if err := api.SaveProxyFilter(c.Request.Context(), b); err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{"proxy_filter": b})
	}
}
