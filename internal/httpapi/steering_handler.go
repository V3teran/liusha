// Package httpapi: 运行中引导 handler（任务详情的「调整目标 / 指派动作」、探索图节点的「否决」）。
//
//	PATCH /tasks/:id                                {goal}                 调整目标，返回更新后的任务
//	POST  /tasks/:id/actions                        {instruction, priority} 指派动作，返回 {id}
//	POST  /tasks/:id/actions/:action_id/reject      {reason}               否决动作
//
// 引导只作用于进行中（含暂停中）的任务：已结束的任务 409（在对话里续接）。

package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/task"
)

// TaskSteeringAPI 是运行中引导的后端依赖（dispatch.Service 满足）：每个操作一个事务，
// 任务行、探索图与时间线回执同时落定。
type TaskSteeringAPI interface {
	AdjustGoal(ctx context.Context, taskID, goal string) error
	AssignAction(ctx context.Context, taskID, instruction string) (string, error)
	RejectAction(ctx context.Context, taskID, actionID, reason string) error
}

// 指派指令与否决原因的长度上限：指令逐字进执行者提示词、原因随节点展示，都是一步之内
// 的短文本（目标的上限见 task.MaxGoalRunes）。
const (
	maxInstructionRunes = 2000
	maxReasonRunes      = 500
)

// boundedText 去首尾空白后校验非空与长度上限，不合法时返回面向操作者的原因。
func boundedText(raw *string, what string, limit int) (string, string) {
	if raw == nil {
		return "", what + "不能为空"
	}
	text := strings.TrimSpace(*raw)
	if text == "" {
		return "", what + "不能为空"
	}
	if len([]rune(text)) > limit {
		return "", what + "过长，请精简"
	}
	return text, ""
}

// respondSteeringErr 把引导错误收敛为状态码：任务已结束、动作已结束 → 409；动作不存在 → 404；
// 任务不存在由 respondErr 收敛为 404。
func respondSteeringErr(c *gin.Context, err error) {
	switch {
	case errors.Is(err, task.ErrNotActive):
		c.JSON(http.StatusConflict, gin.H{"error": "任务已结束，可在对话中续接"})
	case errors.Is(err, explorationgraph.ErrActionSettled):
		c.JSON(http.StatusConflict, gin.H{"error": "动作已结束，无需否决"})
	case errors.Is(err, explorationgraph.ErrActionNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "动作不存在"})
	default:
		respondErr(c, http.StatusInternalServerError, err)
	}
}

// patchTaskHandler 处理 PATCH /tasks/:id {"goal"}：调整目标，返回更新后的任务。
func patchTaskHandler(api TaskSteeringAPI, tasks TasksAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Goal *string `json:"goal"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体非法（需 goal）"})
			return
		}
		goal, invalid := boundedText(req.Goal, "目标", task.MaxGoalRunes)
		if invalid != "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": invalid})
			return
		}
		id := c.Param("id")
		if err := api.AdjustGoal(c.Request.Context(), id, goal); err != nil {
			respondSteeringErr(c, err)
			return
		}
		sm, err := tasks.GetSummary(c.Request.Context(), id)
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, taskSummaryJSON(sm))
	}
}

// assignActionHandler 处理 POST /tasks/:id/actions {"instruction"}：指派动作，先于规划者排出的
// 动作被执行。
func assignActionHandler(api TaskSteeringAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Instruction *string `json:"instruction"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体非法（需 instruction）"})
			return
		}
		instruction, invalid := boundedText(req.Instruction, "指令", maxInstructionRunes)
		if invalid != "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": invalid})
			return
		}
		id, err := api.AssignAction(c.Request.Context(), c.Param("id"), instruction)
		if err != nil {
			respondSteeringErr(c, err)
			return
		}
		c.JSON(http.StatusCreated, gin.H{"id": id})
	}
}

// rejectActionHandler 处理 POST /tasks/:id/actions/:action_id/reject {"reason"}：否决动作。
func rejectActionHandler(api TaskSteeringAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Reason *string `json:"reason"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体非法（需 reason）"})
			return
		}
		reason, invalid := boundedText(req.Reason, "否决原因", maxReasonRunes)
		if invalid != "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": invalid})
			return
		}
		if err := api.RejectAction(c.Request.Context(), c.Param("id"), c.Param("action_id"), reason); err != nil {
			respondSteeringErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
}
