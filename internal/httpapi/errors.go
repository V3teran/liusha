package httpapi

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/V3teran/liusha/internal/configstore"
	fwllm "github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/logx"
	"github.com/V3teran/liusha/internal/override"
	"github.com/V3teran/liusha/internal/skillstore"
)

// apiLog 是 httpapi 包级结构化日志器（logx 同进程共享底层 writer）。
// handler 构造签名只收窄接口、不传 logger，统一走这里。
var apiLog = logx.New("httpapi")

// respondErr 是错误响应的单一出口：结构化记录（方法/路径/状态/错误链）后
// 把 err.Error() 作为中文错误消息回给客户端。本系统是带鉴权的单租户运维控制台，
// 操作者依赖消息文本定位配置/依赖问题（store 层错误均为中文作者消息），故不脱敏；
// 但必须落日志——否则客户端看到错误、服务端无痕迹。
//
// 「资源不存在」（store 以 %w 包装 pgx.ErrNoRows）不是服务端故障：无论调用方按
// 意外失败传入何种 5xx，一律收敛为 404——客户端据此区分「对象已删除/不存在」与
// 「服务出错可重试」，驱动原文（no rows in result set）也不再外透。
func respondErr(c *gin.Context, status int, err error) {
	if status >= http.StatusInternalServerError && isNoRows(err) {
		c.JSON(http.StatusNotFound, gin.H{"error": "资源不存在"})
		return
	}
	// LLM 未配置是客户端可修复的配置问题（错误文本已带配置指引），不是服务端
	// 故障：统一收敛为 400，调用方（前端/脚本）按 4xx 提示而非重试。
	if status >= http.StatusInternalServerError && errors.Is(err, fwllm.ErrNotConfigured) {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// 配置类的客户端错误同样不是服务端故障：提交不合法 → 400；标识冲突、
	// 操作不适用于该类对象（删除内置技能、恢复自建技能的出厂内容）→ 409。
	if status >= http.StatusInternalServerError {
		switch {
		case errors.Is(err, override.ErrInvalid):
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		case errors.Is(err, skillstore.ErrConflict),
			errors.Is(err, configstore.ErrBuiltinSkill),
			errors.Is(err, configstore.ErrCustomSkill):
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
	}
	apiLog.Error().
		Err(err).
		Str("method", c.Request.Method).
		Str("path", c.Request.URL.Path).
		Int("status", status).
		Msg("request failed")
	c.JSON(status, gin.H{"error": err.Error()})
}
