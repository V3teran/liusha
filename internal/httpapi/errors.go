package httpapi

import (
	"github.com/gin-gonic/gin"

	"github.com/V3teran/liusha/internal/logx"
)

// apiLog 是 httpapi 包级结构化日志器（logx 同进程共享底层 writer）。
// handler 构造签名只收窄接口、不传 logger，统一走这里。
var apiLog = logx.New("httpapi")

// respondErr 是错误响应的单一出口：结构化记录（方法/路径/状态/错误链）后
// 把 err.Error() 作为中文错误消息回给客户端。本系统是带鉴权的单租户运维控制台，
// 操作者依赖消息文本定位配置/依赖问题（store 层错误均为中文作者消息），故不脱敏；
// 但必须落日志——否则客户端看到错误、服务端无痕迹。
func respondErr(c *gin.Context, status int, err error) {
	apiLog.Error().
		Err(err).
		Str("method", c.Request.Method).
		Str("path", c.Request.URL.Path).
		Int("status", status).
		Msg("request failed")
	c.JSON(status, gin.H{"error": err.Error()})
}
