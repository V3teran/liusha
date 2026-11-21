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
	"errors"
	"fmt"
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

// TaskDispatchAPI 是「新建任务」的下发窄接口（*dispatch.Service 实现）。
type TaskDispatchAPI interface {
	// DispatchBatch 立即下发：1 assignment(direct, N items) → N task → N conversation。
	DispatchBatch(ctx context.Context, title, description string, briefs []string, scenario *string) (assignmentID string, items []DispatchedTask, err error)
	// DispatchScheduled 建定时模板（不立即跑）；返回模板 id 与首次触发时间。
	DispatchScheduled(ctx context.Context, title, description string, briefs []string, cronExpr string, scenario *string) (scheduleID string, nextRunAt time.Time, err error)
}

// 任务列表分页默认值/上限（对齐 traffic 口径）。
const (
	defaultTaskPageSize = 50
	maxTaskPageSize     = 200
)

// parseTaskFilter 从 query 解析筛选 + 分页。
//
//	task=<任务 UUID> conversation=<对话 UUID>（范围：只看这个任务 / 这个对话的任务；非法 400）
//	source=<direct|proxy> status=<active|completed|aborted> search=<目标/批次名 子串>
//	since=<RFC3339> until=<RFC3339>（按创建时间）；取值写错时 400
//
// page 缺省 1、size clamp 到上限。返回 ok=false 时 400 已写出。
func parseTaskFilter(c *gin.Context, source string) (f task.ListFilter, page, size int, ok bool) {
	taskID, ok := uuidQuery(c, "task")
	if !ok {
		return task.ListFilter{}, 0, 0, false
	}
	conversationID, ok := uuidQuery(c, "conversation")
	if !ok {
		return task.ListFilter{}, 0, 0, false
	}
	page, size = parsePagination(c.Query("page"), c.Query("size"), defaultTaskPageSize, maxTaskPageSize)
	f = task.ListFilter{
		TaskID:         taskID,
		ConversationID: conversationID,
		Source:         source,
		Status:         strings.TrimSpace(c.Query("status")),
		Search:         strings.TrimSpace(c.Query("search")),
		Limit:          size,
		Offset:         (page - 1) * size,
	}
	switch task.Status(f.Status) {
	case "", task.StatusActive, task.StatusCompleted, task.StatusAborted:
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("status 取值无效（可选 %s / %s / %s）", task.StatusActive, task.StatusCompleted, task.StatusAborted)})
		return task.ListFilter{}, 0, 0, false
	}
	since, err := timeQuery(c, "since")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return task.ListFilter{}, 0, 0, false
	}
	until, err := timeQuery(c, "until")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return task.ListFilter{}, 0, 0, false
	}
	if since != nil {
		f.Since = *since
	}
	if until != nil {
		f.Until = *until
	}
	return f, page, size, true
}

// listTasksHandler 处理 GET /tasks?task=&conversation=&source=&status=&search=&page=&size=。
//
// 返回瘦摘要行（含派生列）+ total（同口径），供前端分页表：
//
//	{ "items": [{...summary}], "total": N, "page": P, "size": S }
func listTasksHandler(api TasksAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		source, ok := sourceParam(c)
		if !ok {
			return
		}
		f, page, size, ok := parseTaskFilter(c, source)
		if !ok {
			return
		}

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
	Scenario    *string  `json:"scenario"`    // 任务场景（语义同 ChatRequest.Scenario）
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
				strings.TrimSpace(req.Title), strings.TrimSpace(req.Description), briefs, cronExpr, req.Scenario)
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
			strings.TrimSpace(req.Title), strings.TrimSpace(req.Description), briefs, req.Scenario)
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err) // 目标超长、场景不可用由 respondErr 收敛为 400
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
		"goal":             sm.Goal,
		"target_host":      sm.TargetHost,
		"status":           string(sm.Status),
		"paused":           sm.Paused,
		"assignment_id":    sm.AssignmentID,
		"assignment_title": sm.AssignmentTitle,
		"assignment_index": sm.AssignmentIndex,
		"assignment_total": sm.AssignmentTotal,
		"source":           sm.Source,
		"scheduled":        sm.ScheduleID != "",
		"schedule_id":      sm.ScheduleID,
		"cron":             sm.CronExpr,
		"conversation_id":  sm.ConversationID,
		"scenario":         sm.ScenarioCode,
		"scenario_name":    sm.ScenarioName,
		"scenario_deleted": sm.ScenarioDeleted,
		"finding_count":    sm.FindingCount,
		"step_count":       sm.StepCount,
		"tokens_in":        sm.TokensIn,
		"tokens_out":       sm.TokensOut,
		"created_at":       sm.CreatedAt,
		"ended_at":         sm.EndedAt,
	}
}

// isNoRows 判 pgx 无行（404 映射）。
func isNoRows(err error) bool {
	return err != nil && strings.Contains(err.Error(), pgx.ErrNoRows.Error())
}

// ── 任务控制面 ─────────────────────────────────────────────────────────────

// TaskControlAPI 是任务行操作的后端依赖：pause/resume 是 task.paused 标记的
// CAS 迁移（暂停同步生效，循环在下一控制周期观察到并结束本 run；继续在 run
// 已退出时重投全新 planner 从探索图续跑）；terminate 是 task 行 Abort 终态
// （运行中的循环按行观察退出，与对话页「停止」同一语义）。api 组件注入
// dispatch 侧适配器。
type TaskControlAPI interface {
	PauseTask(ctx context.Context, taskID string) error
	ResumeTask(ctx context.Context, taskID string) error
	TerminateTask(ctx context.Context, taskID string) error
}

// taskControlHandler 处理 POST /tasks/:id/control {"action":"pause|resume|terminate"}。
func taskControlHandler(api TaskControlAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		var req struct {
			Action string `json:"action"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.Action == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体非法（需 action: pause|resume|terminate）"})
			return
		}
		var err error
		switch req.Action {
		case "pause":
			err = api.PauseTask(c.Request.Context(), id)
		case "resume":
			err = api.ResumeTask(c.Request.Context(), id)
		case "terminate":
			err = api.TerminateTask(c.Request.Context(), id)
		default:
			c.JSON(http.StatusBadRequest, gin.H{"error": "未知 action: " + req.Action})
			return
		}
		if err != nil {
			// 状态冲突（终态任务收到暂停/继续、已完成任务收到停止）是客户端
			// 可预期的事实而非服务端故障——映射 409，前端按「任务已结束」提示。
			if errors.Is(err, task.ErrNotActive) || errors.Is(err, task.ErrConflict) {
				respondErr(c, http.StatusConflict, err)
				return
			}
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true, "action": req.Action})
	}
}
