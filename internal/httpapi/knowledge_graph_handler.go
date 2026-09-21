package httpapi

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/V3teran/liusha/internal/knowledgegraph"
)

// KnowledgeGraphAPI 是知识图谱查询的窄接口（由 *knowledgegraph.AdapterStore 实现）
type KnowledgeGraphAPI interface {
	// ListNodesForAPI 按 task_id 和可选 kind 查询节点
	ListNodesForAPI(ctx context.Context, taskID string, kind string) ([]knowledgegraph.Node, error)

	// ListEdgesForAPI 按 task_id 查询边
	ListEdgesForAPI(ctx context.Context, taskID string) ([]knowledgegraph.Edge, error)

	// GetStatsForAPI 返回节点类型统计（e2e 轮询专用）
	GetStatsForAPI(ctx context.Context, taskID string) (map[string]int, error)
}

// GraphStats 是节点类型统计响应（e2e 轮询专用，避免传输大量数据）
type GraphStats struct {
	Objectives   int `json:"objectives"`
	Actions      int `json:"actions"`
	Observations int `json:"observations"`
	Evaluations  int `json:"evaluations"`
	Results      int `json:"results"`
}

// GraphResponse 是完整图谱响应
type GraphResponse struct {
	Nodes []knowledgegraph.Node `json:"nodes"`
	Edges []knowledgegraph.Edge `json:"edges"`
}

// NodesResponse 是按类型筛选节点响应
type NodesResponse struct {
	Nodes []knowledgegraph.Node `json:"nodes"`
}

// getTaskStats 处理 GET /api/v1/tasks/{taskId}/stats
// 返回节点类型统计（e2e 轮询专用）
func getTaskStats(api KnowledgeGraphAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		taskID := c.Param("taskId")
		if taskID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "task_id 不能为空"})
			return
		}

		statsMap, err := api.GetStatsForAPI(c.Request.Context(), taskID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		// 转换为 GraphStats 结构
		stats := GraphStats{
			Objectives:   statsMap["objectives"],
			Actions:      statsMap["actions"],
			Observations: statsMap["observations"],
			Evaluations:  statsMap["evaluations"],
			Results:      statsMap["results"],
		}

		c.JSON(http.StatusOK, stats)
	}
}

// getTaskNodes 处理 GET /api/v1/tasks/{taskId}/nodes?kind=objective
// 按类型筛选节点
func getTaskNodes(api KnowledgeGraphAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		taskID := c.Param("taskId")
		if taskID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "task_id 不能为空"})
			return
		}

		// 可选的 kind 过滤
		kind := c.Query("kind")

		nodes, err := api.ListNodesForAPI(c.Request.Context(), taskID, kind)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, NodesResponse{Nodes: nodes})
	}
}

// getTaskGraph 处理 GET /api/v1/tasks/{taskId}/graph
// 返回完整知识图谱（nodes + edges）
func getTaskGraph(api KnowledgeGraphAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		taskID := c.Param("taskId")
		if taskID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "task_id 不能为空"})
			return
		}

		// 查询所有节点
		nodes, err := api.ListNodesForAPI(c.Request.Context(), taskID, "")
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		// 查询所有边
		edges, err := api.ListEdgesForAPI(c.Request.Context(), taskID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, GraphResponse{
			Nodes: nodes,
			Edges: edges,
		})
	}
}
