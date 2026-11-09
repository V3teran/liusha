// Package httpapi: 系统设置 handler（前端「系统配置」页）。
//
// 三组设置（compaction 会话压缩 / runtime 工具运行时 / proxy_filter 代理流量过滤），
// 路由段即分组取值。生效值 = 出厂默认（代码 + config.yaml 部署值）叠加用户自定义；
// 写入只记录用户改动的字段。读写一律经 settingstore（读走三级缓存，写后广播失效：
// runner 下次现读即新值，proxy 收到 proxy_filter 失效后热换过滤链）。
package httpapi

import (
	"context"
	"net/http"
	"slices"

	"github.com/gin-gonic/gin"

	"github.com/V3teran/liusha/internal/override"
	"github.com/V3teran/liusha/internal/settingstore"
)

// SettingsAPI 是系统设置 handler 依赖的窄接口；*settingstore.Store 自动满足。
type SettingsAPI interface {
	Detail(ctx context.Context, g settingstore.Group) (settingstore.GroupDetail, error)
	Patch(ctx context.Context, g settingstore.Group, changes override.Document) error
	Reset(ctx context.Context, g settingstore.Group, fields ...string) error
}

// settingGroup 解析路由里的分组；未知分组写 404 并返回 ok=false。
func settingGroup(c *gin.Context) (settingstore.Group, bool) {
	g := settingstore.Group(c.Param("group"))
	if !slices.Contains(settingstore.Groups, g) {
		c.JSON(http.StatusNotFound, gin.H{"error": "设置分组不存在（可选 compaction / runtime / proxy_filter）"})
		return "", false
	}
	return g, true
}

// getSettingsHandler 处理 GET /settings/:group → {values, defaults, overrides}。
func getSettingsHandler(api SettingsAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		g, ok := settingGroup(c)
		if !ok {
			return
		}
		respondSettings(c, api, g)
	}
}

// patchSettingsHandler 处理 PATCH /settings/:group。请求体只含改动的字段：数值提交新值，
// 名单提交 {on, off}。与出厂默认相同的字段不算自定义；整组校验通过才写入。
func patchSettingsHandler(api SettingsAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		g, ok := settingGroup(c)
		if !ok {
			return
		}
		var changes override.Document
		if err := c.ShouldBindJSON(&changes); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体非法: " + err.Error()})
			return
		}
		if len(changes) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "没有要修改的字段"})
			return
		}
		if err := api.Patch(c.Request.Context(), g, changes); err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		respondSettings(c, api, g)
	}
}

// resetSettingsHandler 处理 DELETE /settings/:group/overrides[/:field]：恢复出厂默认
// （不带字段 = 整组）。
func resetSettingsHandler(api SettingsAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		g, ok := settingGroup(c)
		if !ok {
			return
		}
		var fields []string
		if f := c.Param("field"); f != "" {
			fields = append(fields, f)
		}
		if err := api.Reset(c.Request.Context(), g, fields...); err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		respondSettings(c, api, g)
	}
}

func respondSettings(c *gin.Context, api SettingsAPI, g settingstore.Group) {
	d, err := api.Detail(c.Request.Context(), g)
	if err != nil {
		respondErr(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, d)
}
