package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/V3teran/liusha/internal/assignment"
)

// requireUUIDParam 在边界校验 UUID 主键路径参数：格式非法直接 400，不让畸形输入
// 下沉到 SQL 层以 22P02 的 500 形态返回（那会把客户端错误伪装成服务端故障）。
func requireUUIDParam(name string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, err := uuid.Parse(c.Param(name)); err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": name + " 不是合法的 UUID"})
			return
		}
		c.Next()
	}
}

// sourceParam 解析可选的 source 筛选参数：空 = 不筛选；非空必须是已定义来源，
// 否则写 400 并返回 ok=false。未知取值若放行会静默返回空列表，调用方无从得知筛选写错。
func sourceParam(c *gin.Context) (source string, ok bool) {
	s := c.Query("source")
	if s == "" || assignment.Source(s).Valid() {
		return s, true
	}
	c.JSON(http.StatusBadRequest, gin.H{"error": "source 取值无效（可选 direct / proxy）", "source": s})
	return "", false
}
