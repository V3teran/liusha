// Package httpapi: 系统配置 handler（前端「系统配置」页）。
//
// 三组业务旋钮对应 migration 0098 的 system_setting 分组 KV：
//   - compaction   ：会话历史压缩（各角色 ReAct 滚动窗口消息条数）
//   - runtime      ：工具运行时（run_command 超时钳制 / 单步 LLM 看门狗 / 输出尾部保留）
//   - proxy_filter ：代理流量过滤规则（黑白名单 + body 上限）
//
// 写路径一律走 settingstore（落 DB + redis 广播失效）：runner 下次现读即拿到最新
// compaction/runtime 旋钮；proxy 进程订阅 proxy_filter 失效后热换过滤链（真热改，无需重启）。
// handler 只做 HTTP 编解码 + 校验，绝不直穿底层 store。

package httpapi

import (
	"context"
	"net/http"

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
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"compaction": v})
	}
}

// putCompactionSettingsHandler 处理 PUT /settings/compaction（全量覆写 + 校验）。
func putCompactionSettingsHandler(api SettingsAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		var b settingstore.CompactionSettings
		if err := c.ShouldBindJSON(&b); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体非法: " + err.Error()})
			return
		}
		// 各角色滚动窗口条数须为正（条数语义：保留 system + 最近 N 条非系统消息）。
		for _, f := range []struct {
			name  string
			value int
		}{
			{"planner_window_messages", b.PlannerWindowMessages},
			{"executor_window_messages", b.ExecutorWindowMessages},
			{"evaluator_window_messages", b.EvaluatorWindowMessages},
			{"monitor_window_messages", b.MonitorWindowMessages},
		} {
			if f.value <= 0 {
				c.JSON(http.StatusBadRequest, gin.H{"error": f.name + " 必须 > 0（该角色 ReAct 滚动窗口保留的消息条数）"})
				return
			}
		}
		if err := api.SaveCompaction(c.Request.Context(), b); err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"compaction": b})
	}
}

// ── runtime（工具运行时旋钮）─────────────────────────────────────────

// getRuntimeSettingsHandler 处理 GET /settings/runtime。
func getRuntimeSettingsHandler(api SettingsAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		v, err := api.Runtime(c.Request.Context())
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"runtime": v})
	}
}

// putRuntimeSettingsHandler 处理 PUT /settings/runtime（全量覆写 + 校验）。
func putRuntimeSettingsHandler(api SettingsAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		var b settingstore.RuntimeSettings
		if err := c.ShouldBindJSON(&b); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体非法: " + err.Error()})
			return
		}
		if b.StepToolTimeoutSeconds <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "step_tool_timeout_seconds 必须 > 0（run_command 超时钳制上限秒数）"})
			return
		}
		if b.StepLLMTimeoutSeconds <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "step_llm_timeout_seconds 必须 > 0（单步 LLM 看门狗超时秒数）"})
			return
		}
		if b.RunTailBytes <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "run_tail_bytes 必须 > 0（stdout/stderr 保留尾部字节数）"})
			return
		}
		if err := api.SaveRuntime(c.Request.Context(), b); err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"runtime": b})
	}
}

// ── proxy_filter(代理流量过滤规则)────────────────────────────────────

// getProxyFilterSettingsHandler 处理 GET /settings/proxy-filter。
func getProxyFilterSettingsHandler(api SettingsAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		v, err := api.ProxyFilter(c.Request.Context())
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"proxy_filter": v})
	}
}

// putProxyFilterSettingsHandler 处理 PUT /settings/proxy-filter（全量覆写 + 校验）。
// 保存后经失效总线广播，proxy 进程热换过滤链（下一条流量即走新规则）。
func putProxyFilterSettingsHandler(api SettingsAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		var b settingstore.ProxyFilterSettings
		if err := c.ShouldBindJSON(&b); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体非法: " + err.Error()})
			return
		}
		// body 上限须为正（LimitReader 语义：0 会截成空 body，几乎必是误填）。
		if b.MaxRequestBodySize <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "max_request_body_size 必须 > 0（请求体切片上限字节）"})
			return
		}
		if b.MaxResponseBodySize <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "max_response_body_size 必须 > 0（响应体切片上限字节）"})
			return
		}
		if err := api.SaveProxyFilter(c.Request.Context(), b); err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"proxy_filter": b})
	}
}
