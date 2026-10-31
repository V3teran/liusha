// Package httpapi: 任务模块 handler（前端 /tasks 页）。
//
// 数据面：GET /tasks（筛选分页列表）+ GET /tasks/:id（详情板），走 task.Store 的
// ListFiltered/CountFiltered/GetSummary（JOIN assignment 派生来源/批次、conversation
// 派生对话链接、finding/探索图 action 计数）。
//
// 下发面：POST /tasks（「新建任务」弹层）——批次名称/描述 + 每行一个目标的 briefs +
// 可选 cron。cron 空 = 立即下发（1 assignment → N task → N conversation）；
// cron 非空 = 建定时模板（到点由 Scheduler 克隆 assignment 展开，不立即跑）。

package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"github.com/V3teran/liusha/internal/task"
)

// TasksAPI 是任务列表/详情查询的窄接口；*task.Store 自动满足。
type TasksAPI interface {
	ListFiltered(ctx context.Context, f task.ListFilter) ([]task.Summary, error)
	CountFiltered(ctx context.Context, f task.ListFilter) (int, error)
	GetSummary(ctx context.Context, id string) (task.Summary, error)
}

// DispatchedTask 是批量下发单个 item 的落库结果（task + 绑定对话）。
type DispatchedTask struct {
	TaskID         string `json:"task_id"`
	ConversationID string `json:"conversation_id"`
}

// TaskDispatchAPI 是「新建任务」的下发窄接口（cmd/api scanAdapter 实现）。
type TaskDispatchAPI interface {
	// DispatchBatch 立即下发：1 assignment(manual, N items) → N task → N conversation。
	DispatchBatch(ctx context.Context, title, description string, briefs []string) (assignmentID string, items []DispatchedTask, err error)
	// DispatchScheduled 建定时模板（不立即跑）；返回模板 id 与首次触发时间。
	DispatchScheduled(ctx context.Context, title, description string, briefs []string, cronExpr string) (scheduleID string, nextRunAt time.Time, err error)
}

// 任务列表分页默认值/上限（对齐 traffic 口径）。
const (
	defaultTaskPageSize = 50
	maxTaskPageSize     = 200
)

// parseTaskFilter 从 query 解析筛选 + 分页。
//
//	source=<manual|auto> status=<active|completed|aborted> search=<brief/批次ID 子串>
//	since=<RFC3339> until=<RFC3339>（按创建时间；解析失败视为未传，退化成不筛）
//
// 坏参数退化成不筛（对齐 parseTrafficFilter），page 缺省 1、size clamp 到上限。
func parseTaskFilter(c *gin.Context) (task.ListFilter, int, int) {
	page, size := parsePagination(c.Query("page"), c.Query("size"), defaultTaskPageSize, maxTaskPageSize)
	f := task.ListFilter{
		Source: strings.TrimSpace(c.Query("source")),
		Status: strings.TrimSpace(c.Query("status")),
		Search: strings.TrimSpace(c.Query("search")),
		Limit:  size,
		Offset: (page - 1) * size,
	}
	if s := c.Query("since"); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			f.Since = t
		}
	}
	if s := c.Query("until"); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			f.Until = t
		}
	}
	return f, page, size
}

// listTasksHandler 处理 GET /tasks?source=&status=&search=&page=&size=。
//
// 返回瘦摘要行（含派生列）+ total（同口径），供前端分页表：
//
//	{ "items": [{...summary}], "total": N, "page": P, "size": S }
func listTasksHandler(api TasksAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		f, page, size := parseTaskFilter(c)

		rows, err := api.ListFiltered(ctx, f)
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		total, err := api.CountFiltered(ctx, f)
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"items": taskSummariesJSON(rows),
			"total": total, "page": page, "size": size,
		})
	}
}

// getTaskHandler 处理 GET /tasks/:id：单任务摘要（详情板）。
func getTaskHandler(api TasksAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		sm, err := api.GetSummary(c.Request.Context(), c.Param("id"))
		if err != nil {
			if isNoRows(err) {
				c.JSON(http.StatusNotFound, gin.H{"error": "task not found", "id": c.Param("id")})
				return
			}
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, taskSummaryJSON(sm))
	}
}

// taskCreateRequest 是 POST /tasks 请求体（「新建任务」弹层直出）。
type taskCreateRequest struct {
	Title       string   `json:"title"`       // 批次名称
	Description string   `json:"description"` // 批次描述（可空）
	Briefs      []string `json:"briefs"`      // 每行一个任务目标（已分行）
	Cron        string   `json:"cron"`        // 可空 = 立即下发；非空 = 定时模板（5 段 cron）
}

// createTasksHandler 处理 POST /tasks。
func createTasksHandler(api TaskDispatchAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req taskCreateRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体格式错误: " + err.Error()})
			return
		}
		// briefs 规整：去空白行 + TrimSpace（前端按行拆好；这里兜底同语义拆分）。
		briefs := normalizeBriefs(req.Briefs)
		if len(briefs) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "briefs 不能为空：每行一个任务目标"})
			return
		}
		if len(briefs) > 100 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "单批次任务数超上限（100）"})
			return
		}

		if cronExpr := strings.TrimSpace(req.Cron); cronExpr != "" {
			scheduleID, nextRunAt, err := api.DispatchScheduled(c.Request.Context(),
				strings.TrimSpace(req.Title), strings.TrimSpace(req.Description), briefs, cronExpr)
			if err != nil {
				respondErr(c, http.StatusBadRequest, err) // cron 非法等入参错
				return
			}
			c.JSON(http.StatusCreated, gin.H{
				"scheduled":   true,
				"schedule_id": scheduleID,
				"next_run_at": nextRunAt,
				"task_count":  len(briefs),
			})
			return
		}

		assignmentID, items, err := api.DispatchBatch(c.Request.Context(),
			strings.TrimSpace(req.Title), strings.TrimSpace(req.Description), briefs)
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusCreated, gin.H{
			"scheduled":     false,
			"assignment_id": assignmentID,
			"task_count":    len(items),
			"tasks":         items,
		})
	}
}

// normalizeBriefs 兜底规整：接受前端已拆行的数组，也接受整段文本（按 \n 再拆），
// 去空行 + TrimSpace——「每行一个任务」语义在入口处唯一化。
func normalizeBriefs(in []string) []string {
	var out []string
	for _, s := range in {
		for _, line := range strings.Split(s, "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				out = append(out, line)
			}
		}
	}
	return out
}

// taskSummariesJSON / taskSummaryJSON 把 Summary 序列化成 wire 形状（snake_case，
// 空切片不返回 null）。
func taskSummariesJSON(rows []task.Summary) []gin.H {
	items := make([]gin.H, 0, len(rows))
	for _, sm := range rows {
		items = append(items, taskSummaryJSON(sm))
	}
	return items
}

func taskSummaryJSON(sm task.Summary) gin.H {
	return gin.H{
		"id":               sm.ID,
		"brief":            sm.Brief,
		"target_host":      sm.TargetHost,
		"status":           string(sm.Status),
		"assignment_id":    sm.AssignmentID,
		"assignment_index": sm.AssignmentIndex,
		"assignment_total": sm.AssignmentTotal,
		"source":           sm.Source,
		"scheduled":        sm.Scheduled,
		"cron":             sm.CronExpr,
		"conversation_id":  sm.ConversationID,
		"finding_count":    sm.FindingCount,
		"step_count":       sm.StepCount,
		"created_at":       sm.CreatedAt,
		"ended_at":         sm.EndedAt,
	}
}

// isNoRows 判 pgx 无行（404 映射）。
func isNoRows(err error) bool {
	return err != nil && strings.Contains(err.Error(), pgx.ErrNoRows.Error())
}
