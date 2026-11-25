// Package httpapi: 场景 handler（前端「场景」页）。
//
// 场景是任务所处情境的说明，建任务时快照到任务行。内置场景（出厂种子）可编辑、停用、恢复出厂、
// 删除（升级不再补回，可在「已删除的内置」里恢复）；自建场景可增删改。被定时模板引用的场景不可
// 删除、停用（409）。
package httpapi

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/V3teran/liusha/internal/configstore"
	"github.com/V3teran/liusha/internal/override"
	"github.com/V3teran/liusha/internal/scenariostore"
)

// ScenarioAPI 是场景 handler 依赖的窄接口；*configstore.Store 自动满足。
type ScenarioAPI interface {
	ScenarioDetails(ctx context.Context) ([]configstore.ScenarioDetail, error)
	ScenarioDetail(ctx context.Context, code string) (configstore.ScenarioDetail, error)
	CreateScenario(ctx context.Context, sc scenariostore.Scenario) (scenariostore.Scenario, error)
	PatchScenario(ctx context.Context, code string, changes override.Document) (scenariostore.Scenario, error)
	ResetScenario(ctx context.Context, code string, fields ...string) (scenariostore.Scenario, error)
	DeleteScenario(ctx context.Context, code string) error
	DeletedScenarios(ctx context.Context) ([]scenariostore.Scenario, error)
	RestoreScenario(ctx context.Context, code string) (scenariostore.Scenario, error)
}

// listScenariosHandler 处理 GET /scenarios?search=&page=&size= → {scenarios, total}。
// 每项附已偏离出厂的字段名；未带 page 返回全部（下发入口的场景选择框用它）。
// 带 deleted=true 时返回被删除、可恢复的内置场景（内容为当前出厂）。
func listScenariosHandler(api ScenarioAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Query("deleted") == "true" {
			deleted, err := api.DeletedScenarios(c.Request.Context())
			if err != nil {
				respondErr(c, http.StatusInternalServerError, err)
				return
			}
			items := make([]gin.H, 0, len(deleted))
			for _, sc := range deleted {
				items = append(items, scenarioJSON(sc))
			}
			c.JSON(http.StatusOK, gin.H{"scenarios": items, "total": len(items)})
			return
		}
		all, err := api.ScenarioDetails(c.Request.Context())
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		q := c.Query("search")
		items := make([]gin.H, 0, len(all))
		for _, d := range all {
			sc := d.Scenario
			if !matchesQuery(q, sc.Code, sc.Name, sc.Description) {
				continue
			}
			item := scenarioJSON(sc)
			item["customized"] = customizedFields(d.Overrides)
			item["shadows_builtin"] = d.Shadows
			items = append(items, item)
		}
		page, total := pageOf(c, items, defaultConfigPageSize, maxConfigPageSize)
		c.JSON(http.StatusOK, gin.H{"scenarios": page, "total": total})
	}
}

// getScenarioHandler 处理 GET /scenarios/:code → 场景、出厂内容（内置）与各字段相对出厂的状态。
func getScenarioHandler(api ScenarioAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		respondScenarioDetail(c, api, c.Param("code"))
	}
}

// createScenarioHandler 处理 POST /scenarios：新建自建场景。与内置或已有场景同名 → 409。
func createScenarioHandler(api ScenarioAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Code        string `json:"code"`
			Name        string `json:"name"`
			Description string `json:"description"`
			Body        string `json:"body"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体非法: " + err.Error()})
			return
		}
		created, err := api.CreateScenario(c.Request.Context(), scenariostore.Scenario{
			Code: generatedCode(req.Code, req.Name), Name: req.Name, Description: req.Description, Body: req.Body, Enabled: true,
		})
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusCreated, scenarioJSON(created))
	}
}

// patchScenarioHandler 处理 PATCH /scenarios/:code。请求体只含改动的字段（name/description/body/enabled）。
func patchScenarioHandler(api ScenarioAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		var changes override.Document
		if err := c.ShouldBindJSON(&changes); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体非法: " + err.Error()})
			return
		}
		if len(changes) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "没有要修改的字段"})
			return
		}
		if _, err := api.PatchScenario(c.Request.Context(), c.Param("code"), changes); err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		respondScenarioDetail(c, api, c.Param("code"))
	}
}

// resetScenarioHandler 处理 DELETE /scenarios/:code/overrides[/:field]：内置场景恢复出厂内容。
func resetScenarioHandler(api ScenarioAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		var fields []string
		if f := c.Param("field"); f != "" {
			fields = append(fields, f)
		}
		if _, err := api.ResetScenario(c.Request.Context(), c.Param("code"), fields...); err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		respondScenarioDetail(c, api, c.Param("code"))
	}
}

// deleteScenarioHandler 处理 DELETE /scenarios/:code：删除场景（内置记删除标记、可恢复）；
// 被定时模板引用 → 409。已建任务读自身快照，不受影响。
func deleteScenarioHandler(api ScenarioAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := api.DeleteScenario(c.Request.Context(), c.Param("code")); err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		c.Status(http.StatusNoContent)
	}
}

// restoreScenarioHandler 处理 POST /scenarios/:code/restore：把删除的内置场景恢复成当前出厂内容。
// 不是已删除的内置场景 → 404。
func restoreScenarioHandler(api ScenarioAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, err := api.RestoreScenario(c.Request.Context(), c.Param("code")); err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		respondScenarioDetail(c, api, c.Param("code"))
	}
}

func respondScenarioDetail(c *gin.Context, api ScenarioAPI, code string) {
	d, err := api.ScenarioDetail(c.Request.Context(), code)
	if err != nil {
		respondErr(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"scenario":        scenarioJSON(d.Scenario),
		"defaults":        d.Defaults,
		"overrides":       d.Overrides,
		"shadows_builtin": d.Shadows,
	})
}

// scenarioJSON 是场景的单一序列化点。
func scenarioJSON(sc scenariostore.Scenario) gin.H {
	return gin.H{
		"code": sc.Code, "name": sc.Name, "description": sc.Description, "body": sc.Body,
		"enabled": sc.Enabled, "is_builtin": sc.IsBuiltin,
		"is_default": sc.Code == scenariostore.DefaultCode,
	}
}

// generatedCode 落定新建资源的标识符：请求带了按请求（兼容显式寻址名），缺省由
// 服务端生成——slug 化名称（小写字母数字连字符）并拼随机后缀防撞；名称无法 slug
// （纯中文等）时用短随机串。code 是可读的外部契约（agent 名单、定时模板引用它），
// 不是内部身份，不该让用户手填或看到。
func generatedCode(req, name string) string {
	req = strings.TrimSpace(req)
	if req != "" {
		return req
	}
	slug := nameSlug(name)
	suffix := strconv.FormatInt(time.Now().UnixNano()%1e6, 36)
	if slug == "" {
		return "c-" + suffix
	}
	if len(slug) > 40 {
		slug = slug[:40]
	}
	return slug + "-" + suffix
}

// nameSlug 取名称里的 ASCII 字母数字序列以连字符相连（小写化）；无可用序列返回空串。
func nameSlug(name string) string {
	parts := nonAlphaNumeric.Split(strings.ToLower(strings.TrimSpace(name)), -1)
	var kept []string
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, "-")
}

var nonAlphaNumeric = regexp.MustCompile(`[^a-z0-9]+`)
