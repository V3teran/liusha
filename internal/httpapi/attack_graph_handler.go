package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"github.com/V3teran/liusha/internal/task"
	"github.com/V3teran/liusha/internal/worldmodel"
)

// WorldModelAPI 是攻击图（世界模型）读出的窄接口。
// *worldmodel.Store 满足此接口。
type WorldModelAPI interface {
	ListNodesByKind(ctx context.Context, taskID string, kind worldmodel.NodeKind) ([]worldmodel.Node, error)
	ListEdgesFrom(ctx context.Context, taskID, srcID string) ([]worldmodel.Edge, error)
	ListEdgesTo(ctx context.Context, taskID, dstID string) ([]worldmodel.Edge, error)
}

// TaskScanResolver 把前端选中的 task 反解为其所属 assignment（=图的 scan_id）。
type TaskScanResolver interface {
	GetByID(ctx context.Context, id string) (task.Task, error)
}

// attackGraphNodeDTO 是 wm_node 的对外形状
type attackGraphNodeDTO struct {
	ID         string                  `json:"id"`
	Kind       worldmodel.NodeKind     `json:"kind"`
	Content    json.RawMessage         `json:"content"`
	State      *worldmodel.State       `json:"state,omitempty"`       // Move 专用
	Complexity *worldmodel.Complexity  `json:"complexity,omitempty"`  // Move 专用
	Confidence *worldmodel.Confidence  `json:"confidence,omitempty"`  // Observation/Discovery 专用
	Priority   int                     `json:"priority"`
	CreatedAt  string                  `json:"created_at"`
	UpdatedAt  string                  `json:"updated_at"`
}

// attackGraphEdgeDTO 是 wm_edge 的对外形状
type attackGraphEdgeDTO struct {
	SrcID  string              `json:"source"` // React Flow 使用 source/target
	Rel    worldmodel.Relation `json:"rel"`
	DstID  string              `json:"target"`
	Attrs  json.RawMessage     `json:"attrs"`
}

// attackGraphVerificationDTO 是 wm_verification 的对外形状
type attackGraphVerificationDTO struct {
	ID         string                   `json:"id"`
	NodeID     string                   `json:"node_id"`
	Primitives json.RawMessage          `json:"primitives"`
	Outcome    worldmodel.VerifyOutcome `json:"outcome"`
	Evidence   json.RawMessage          `json:"evidence"`
	DurationMs int64                    `json:"duration_ms"`
	CreatedAt  string                   `json:"created_at"`
}

// attackGraphResponse 是 GET /attack_graph/:task_id 的响应封套
type attackGraphResponse struct {
	TaskID        string                        `json:"task_id"`
	ScanID        string                        `json:"scan_id"`
	Nodes         []attackGraphNodeDTO          `json:"nodes"`
	Edges         []attackGraphEdgeDTO          `json:"edges"`
	Verifications []attackGraphVerificationDTO  `json:"verifications"`
}

// attackGraphHandler 处理 GET /attack_graph/:task_id
//
// 返回攻击图（世界模型投影）：目标、Move、观察、发现及其关系边。
func attackGraphHandler(wm WorldModelAPI, resolver TaskScanResolver) gin.HandlerFunc {
	return func(c *gin.Context) {
		taskID := c.Param("task_id")
		if taskID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "task_id required"})
			return
		}

		// 解析 task → assignment (scan_id)
		t, err := resolver.GetByID(c.Request.Context(), taskID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				c.JSON(http.StatusNotFound, gin.H{"error": "task not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		scanID := t.AssignmentID
		if scanID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "task has no assignment_id"})
			return
		}

		// 读取所有类型的节点
		var allNodes []worldmodel.Node
		for _, kind := range []worldmodel.NodeKind{
			worldmodel.KindObjective,
			worldmodel.KindMove,
			worldmodel.KindObservation,
			worldmodel.KindDiscovery,
		} {
			nodes, err := wm.ListNodesByKind(c.Request.Context(), scanID, kind)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
			allNodes = append(allNodes, nodes...)
		}

		// 转换为 DTO
		nodesDTOs := make([]attackGraphNodeDTO, 0, len(allNodes))
		for _, n := range allNodes {
			nodesDTOs = append(nodesDTOs, attackGraphNodeDTO{
				ID:         n.ID,
				Kind:       n.Kind,
				Content:    n.Content,
				State:      n.State,
				Complexity: n.Complexity,
				Confidence: n.Confidence,
				Priority:   n.Priority,
				CreatedAt:  n.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
				UpdatedAt:  n.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
			})
		}

		// 读取边（简化：只从每个节点读出边）
		edgeMap := make(map[string]bool)
		var edgesDTOs []attackGraphEdgeDTO
		for _, n := range allNodes {
			edges, err := wm.ListEdgesFrom(c.Request.Context(), scanID, n.ID)
			if err != nil {
				continue // 非致命错误，继续
			}
			for _, e := range edges {
				key := e.SrcID + "-" + string(e.Rel) + "-" + e.DstID
				if edgeMap[key] {
					continue // 去重
				}
				edgeMap[key] = true
				edgesDTOs = append(edgesDTOs, attackGraphEdgeDTO{
					SrcID: e.SrcID,
					Rel:   e.Rel,
					DstID: e.DstID,
					Attrs: e.Attrs,
				})
			}
		}

		// 返回响应（暂时不返回 verifications，需要额外查询）
		resp := attackGraphResponse{
			TaskID:        taskID,
			ScanID:        scanID,
			Nodes:         nodesDTOs,
			Edges:         edgesDTOs,
			Verifications: []attackGraphVerificationDTO{}, // TODO: 需要添加 ListVerifications 方法
		}

		c.JSON(http.StatusOK, resp)
	}
}
