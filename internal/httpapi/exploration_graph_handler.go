package httpapi

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/V3teran/liusha/internal/explorationgraph"
)

// ExplorationGraphAPI 是探索图查询的窄接口（由 *explorationgraph.Store 实现）
type ExplorationGraphAPI interface {
	// ListNodesByConversationID 按 conversation_id 查询节点（经会话关联的 task）
	ListNodesByConversationID(ctx context.Context, conversationID string, kind string) ([]explorationgraph.Node, error)

	// ListEdgesByConversationID 按 conversation_id 查询边（经会话关联的 task）
	ListEdgesByConversationID(ctx context.Context, conversationID string) ([]explorationgraph.Edge, error)
}

// GraphResponse 是完整图谱响应
type GraphResponse struct {
	Nodes []explorationgraph.Node `json:"nodes"`
	Edges []explorationgraph.Edge `json:"edges"`
}

// NodesResponse 是按类型筛选节点响应
type NodesResponse struct {
	Nodes []explorationgraph.Node `json:"nodes"`
}

// getConversationGraph 处理 GET /api/conversations/{id}/exploration/graph
// 返回对话的完整探索图（nodes + edges）
func getConversationGraph(api ExplorationGraphAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		conversationID := c.Param("id")
		if conversationID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "conversation_id 不能为空"})
			return
		}

		// 查询所有节点
		nodes, err := api.ListNodesByConversationID(c.Request.Context(), conversationID, "")
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}

		// 查询所有边
		edges, err := api.ListEdgesByConversationID(c.Request.Context(), conversationID)
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}

		c.JSON(http.StatusOK, GraphResponse{
			Nodes: nodes,
			Edges: edges,
		})
	}
}
