package httpapi

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/V3teran/liusha/internal/assignment"
	"github.com/V3teran/liusha/internal/cronschedule"
)

// SchedulesAPI 是定时模板管理的窄接口（*cronschedule.Store 满足）。模板由「新建任务」的
// 定时下发创建（POST /tasks 带 cron）；这里管理它的后续生命周期：查看、启停、删除。
// 不存在的 id 由 store 包装 pgx.ErrNoRows，经 respondErr 收敛为 404。
type SchedulesAPI interface {
	List(ctx context.Context) ([]cronschedule.Schedule, error)
	SetEnabled(ctx context.Context, id string, enabled bool) (cronschedule.Schedule, error)
	Delete(ctx context.Context, id string) error
}

// listSchedulesHandler 处理 GET /schedules：全部定时模板（含已停用），新建的在前。
func listSchedulesHandler(api SchedulesAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		rows, err := api.List(c.Request.Context())
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		items := make([]gin.H, 0, len(rows))
		for _, r := range rows {
			items = append(items, scheduleJSON(r))
		}
		c.JSON(http.StatusOK, gin.H{"items": items})
	}
}

// patchScheduleBody 是 PATCH /schedules/:id 的请求体：启停是模板唯一可变的状态，enabled 必填。
type patchScheduleBody struct {
	Enabled *bool `json:"enabled"`
}

// patchScheduleHandler 处理 PATCH /schedules/:id：启用 / 停用。重新启用时下次触发从当前
// 时刻按 cron 重算（停用期间错过的不补跑），返回写后的模板。
func patchScheduleHandler(api SchedulesAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		var b patchScheduleBody
		if err := c.ShouldBindJSON(&b); err != nil || b.Enabled == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体须为 {\"enabled\": true|false}"})
			return
		}
		sc, err := api.SetEnabled(c.Request.Context(), c.Param("id"), *b.Enabled)
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"schedule": scheduleJSON(sc)})
	}
}

// deleteScheduleHandler 处理 DELETE /schedules/:id：删除模板，已触发的任务保留。
func deleteScheduleHandler(api SchedulesAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := api.Delete(c.Request.Context(), c.Param("id")); err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"deleted": true})
	}
}

// scheduleJSON 是定时模板响应的单一序列化点：briefs 由 payload 展开（每条到点展开成一个
// 任务），解析失败记告警并按空清单下发，不拖垮整个列表。
func scheduleJSON(s cronschedule.Schedule) gin.H {
	var items []assignment.Item
	if err := json.Unmarshal(s.Payload, &items); err != nil {
		apiLog.Warn().Err(err).Str("schedule_id", s.ID).Msg("定时模板 payload 解析失败，briefs 按空下发")
	}
	briefs := make([]string, 0, len(items))
	for _, it := range items {
		briefs = append(briefs, it.Brief)
	}
	return gin.H{
		"id":          s.ID,
		"title":       s.Title,
		"description": s.Description,
		"cron":        s.CronExpr,
		"enabled":     s.Enabled,
		"briefs":      briefs,
		"next_run_at": s.NextRunAt,
		"last_run_at": s.LastRunAt,
		"created_at":  s.CreatedAt,
	}
}
