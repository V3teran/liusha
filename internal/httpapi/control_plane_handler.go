package httpapi

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/V3teran/liusha/internal/controlplane"
	"github.com/V3teran/liusha/internal/logx"
)

var controlLog = logx.New("httpapi.control")

// ControlPlaneAPI 定义任务控制平面接口
type ControlPlaneAPI interface {
	Create(ctx context.Context, taskID string, command controlplane.Command, payload json.RawMessage) (uuid.UUID, error)
	ListByTask(ctx context.Context, taskID string, limit int) ([]controlplane.ControlEvent, error)
	GetByID(ctx context.Context, id uuid.UUID) (controlplane.ControlEvent, error)
}

// CreateControlEventRequest 创建控制事件的请求
type CreateControlEventRequest struct {
	Command string          `json:"command" binding:"required"`
	Payload json.RawMessage `json:"payload"`
}

// handleCreateControlEvent 创建控制事件
// POST /tasks/:taskID/control
func handleCreateControlEvent(cp ControlPlaneAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		taskID := c.Param("taskID")
		if taskID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "task_id required"})
			return
		}

		var req CreateControlEventRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}

		// 验证 command
		command := controlplane.Command(req.Command)
		switch command {
		case controlplane.CommandAdjustGoal,
			controlplane.CommandInjectMove,
			controlplane.CommandPause,
			controlplane.CommandResume,
			controlplane.CommandTerminate:
			// 有效命令
		default:
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid command"})
			return
		}

		// 验证 payload（根据 command 类型）
		if err := validateControlPayload(command, req.Payload); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		// 创建控制事件
		eventID, err := cp.Create(c.Request.Context(), taskID, command, req.Payload)
		if err != nil {
			controlLog.Error().Err(err).Str("task_id", taskID).Msg("create control event failed")
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		controlLog.Info().
			Str("task_id", taskID).
			Str("event_id", eventID.String()).
			Str("command", req.Command).
			Msg("control event created")

		c.JSON(http.StatusCreated, gin.H{
			"event_id": eventID.String(),
		})
	}
}

// handleListControlEvents 列出任务的控制事件
// GET /tasks/:taskID/control
func handleListControlEvents(cp ControlPlaneAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		taskID := c.Param("taskID")
		if taskID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "task_id required"})
			return
		}

		events, err := cp.ListByTask(c.Request.Context(), taskID, 100)
		if err != nil {
			controlLog.Error().Err(err).Str("task_id", taskID).Msg("list control events failed")
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"events": events,
		})
	}
}

// handleGetControlEvent 获取单个控制事件
// GET /control-events/:eventID
func handleGetControlEvent(cp ControlPlaneAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		eventIDStr := c.Param("eventID")

		eventID, err := uuid.Parse(eventIDStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid event_id"})
			return
		}

		event, err := cp.GetByID(c.Request.Context(), eventID)
		if err != nil {
			controlLog.Error().Err(err).Str("event_id", eventIDStr).Msg("get control event failed")
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		c.JSON(http.StatusOK, event)
	}
}

// validateControlPayload 验证控制命令的 payload
func validateControlPayload(command controlplane.Command, payload json.RawMessage) error {
	switch command {
	case controlplane.CommandAdjustGoal:
		var p controlplane.AdjustGoalPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return err
		}
		if p.NewGoal == "" {
			return gin.Error{Err: nil, Type: gin.ErrorTypeBind, Meta: "new_goal required"}
		}

	case controlplane.CommandInjectMove:
		var p controlplane.InjectMovePayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return err
		}
		if p.Kind == "" {
			return gin.Error{Err: nil, Type: gin.ErrorTypeBind, Meta: "kind required"}
		}
		if len(p.Target) == 0 {
			return gin.Error{Err: nil, Type: gin.ErrorTypeBind, Meta: "target required"}
		}

	case controlplane.CommandPause, controlplane.CommandResume, controlplane.CommandTerminate:
		// 这些命令不需要 payload
	}

	return nil
}
