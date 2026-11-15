package httpapi

import (
	"context"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/task"
)

// ExplorationGraphAPI 是探索图读取的窄接口（*explorationgraph.Store 满足）。探索图属于任务，
// 一律按任务 ID 取。
type ExplorationGraphAPI interface {
	ListNodesByTask(ctx context.Context, taskID string, kind string) ([]explorationgraph.Node, error)
	ListEdgesByTask(ctx context.Context, taskID string) ([]explorationgraph.Edge, error)
	// ListVerificationsByTask 读验证审计链（时间倒序；每条的 node_id 指向被验证的假设节点）。
	ListVerificationsByTask(ctx context.Context, taskID string, limit int) ([]explorationgraph.Verification, error)
}

// TaskReader 读任务行（*task.Store 满足）。任务名下资源的接口据此区分「任务不存在」（404）
// 与「任务还没有数据」（空结果）。不存在时返回包裹 pgx.ErrNoRows 的错误。
type TaskReader interface {
	GetByID(ctx context.Context, id string) (task.Task, error)
}

// GraphResponse 是完整探索图响应。
type GraphResponse struct {
	Nodes []explorationgraph.Node `json:"nodes"`
	Edges []explorationgraph.Edge `json:"edges"`
}

// defaultVerificationLimit 是验证审计链缺省读取的条数。
const defaultVerificationLimit = 200

// requireTask 确认路径里的任务存在：不存在 404，查询失败 500；返回 false 时响应已写出。
func requireTask(c *gin.Context, tasks TaskReader) bool {
	if _, err := tasks.GetByID(c.Request.Context(), c.Param("id")); err != nil {
		respondErr(c, http.StatusInternalServerError, err)
		return false
	}
	return true
}

// getTaskGraphHandler 处理 GET /tasks/:id/exploration/graph：任务的完整探索图（节点 + 边）。
func getTaskGraphHandler(tasks TaskReader, graph ExplorationGraphAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !requireTask(c, tasks) {
			return
		}
		ctx, taskID := c.Request.Context(), c.Param("id")
		nodes, err := graph.ListNodesByTask(ctx, taskID, "")
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		edges, err := graph.ListEdgesByTask(ctx, taskID)
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		if nodes == nil {
			nodes = []explorationgraph.Node{}
		}
		if edges == nil {
			edges = []explorationgraph.Edge{}
		}
		c.JSON(http.StatusOK, GraphResponse{Nodes: nodes, Edges: edges})
	}
}

// getTaskVerificationsHandler 处理 GET /tasks/:id/exploration/verifications?limit=：任务的验证
// 审计链（坐实与证伪的机器证据，node_id 回挂图节点）。
func getTaskVerificationsHandler(tasks TaskReader, graph ExplorationGraphAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !requireTask(c, tasks) {
			return
		}
		limit := defaultVerificationLimit
		if n, err := strconv.Atoi(c.Query("limit")); err == nil && n > 0 {
			limit = n
		}
		verifications, err := graph.ListVerificationsByTask(c.Request.Context(), c.Param("id"), limit)
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		if verifications == nil {
			verifications = []explorationgraph.Verification{}
		}
		c.JSON(http.StatusOK, gin.H{"verifications": verifications})
	}
}
