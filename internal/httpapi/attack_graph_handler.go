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

// WorldModelAPI 是攻击图（L3 世界模型）读出的窄接口，handler 只依赖它。
// *worldmodel.Store 自动满足。图按 scan_id（=assignment_id）归属。
type WorldModelAPI interface {
	ListNodes(ctx context.Context, taskID string) ([]worldmodel.Node, error)
	ListEdges(ctx context.Context, taskID string) ([]worldmodel.Edge, error)
	ListVerifications(ctx context.Context, taskID string) ([]worldmodel.Verification, error)
}

// TaskScanResolver 把前端选中的 task 反解为其所属 assignment（=图的 scan_id）。
// *task.Store 自动满足。前端选 task 不变，图按交战聚合的语义由后端解析承担。
type TaskScanResolver interface {
	GetByID(ctx context.Context, id string) (task.Task, error)
}

// attackGraphNodeDTO 是 wm_node 的对外形状。model.Node 无 JSON tag（领域层不背序列化契约），
// 读出层显式建 DTO 定契约：seq 作对外稳定短号，ref 三元组多态目标，attrs 原样透传（形状由 kind 定）。
type attackGraphNodeDTO struct {
	ID         string              `json:"id"`
	Seq        int64               `json:"seq"`
	Kind       worldmodel.NodeKind `json:"kind"`
	Ref        worldmodel.TargetRef `json:"ref"`
	Attrs      json.RawMessage     `json:"attrs"`
	Confidence worldmodel.Confidence `json:"confidence"`
	VerifiedBy *string             `json:"verified_by,omitempty"`
}

// attackGraphEdgeDTO 是 wm_edge 的对外形状。领域层用 Src/Dst，前端 React Flow 用 source/target——
// 在此单点转换，前端不必知道后端字段名。
type attackGraphEdgeDTO struct {
	ID     string           `json:"id"`
	Rel    worldmodel.EdgeRel `json:"rel"`
	Source string           `json:"source"`
	Target string           `json:"target"`
	Attrs  json.RawMessage  `json:"attrs"`
}

// attackGraphVerificationDTO 是 wm_verification 的对外形状——取证链（可复现交付 + 合规审计的证据源）。
type attackGraphVerificationDTO struct {
	ID         string                   `json:"id"`
	LeadID     string                   `json:"lead_id"`
	Primitives json.RawMessage          `json:"primitives"`
	Outcome    worldmodel.VerifyOutcome `json:"outcome"`
	Evidence   json.RawMessage          `json:"evidence"`
	DurationMs int64                    `json:"duration_ms"`
	CreatedAt  string                   `json:"created_at"`
}

// attackGraphResponse 是 GET /attack_graph/:task_id 的响应封套。
type attackGraphResponse struct {
	TaskID        string                       `json:"task_id"`
	ScanID        string                       `json:"scan_id"` // =assignment_id，图归属键
	Nodes         []attackGraphNodeDTO         `json:"nodes"`
	Edges         []attackGraphEdgeDTO         `json:"edges"`
	Verifications []attackGraphVerificationDTO `json:"verifications"`
}

// attackGraphHandler 处理 GET /attack_graph/:task_id。
//
// 返回攻击图（L3 世界模型投影）：已确证/假定的世界状态节点（target/asset/credential/access/finding）
// + 关系边（derives 认知因果 / enables 攻击链 / on 归属）+ Verifier 取证链。
// 与旧「思维链 read-model」不同——本图只落 Verifier 坐实的真相，是可复现交付的权威视图。
//
// 前端选 task，后端按 task→assignment 解析出图的 scan_id（一交战一图，跨多阶段 task）。
func attackGraphHandler(wm WorldModelAPI, resolver TaskScanResolver) gin.HandlerFunc {
	return func(c *gin.Context) {
		tid := c.Param("task_id")
		if tid == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "task_id required"})
			return
		}
		ctx := c.Request.Context()

		tk, err := resolver.GetByID(ctx, tid)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				c.JSON(http.StatusNotFound, gin.H{"error": "task not found", "task_id": tid})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		taskID := tk.AssignmentID

		nodes, err := wm.ListNodes(ctx, taskID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		edges, err := wm.ListEdges(ctx, taskID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		vers, err := wm.ListVerifications(ctx, taskID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, attackGraphResponse{
			TaskID:        tid,
			ScanID:        taskID,
			Nodes:         nodesToDTO(nodes),
			Edges:         edgesToDTO(edges),
			Verifications: verificationsToDTO(vers),
		})
	}
}

// nodesToDTO 投影节点并归一 nil 为空数组（前端对 nodes 直接迭代，null 会崩）。
func nodesToDTO(nodes []worldmodel.Node) []attackGraphNodeDTO {
	out := make([]attackGraphNodeDTO, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, attackGraphNodeDTO{
			ID:         n.ID,
			Seq:        n.Seq,
			Kind:       n.Kind,
			Ref:        n.Ref,
			Attrs:      n.Attrs,
			Confidence: n.Confidence,
			VerifiedBy: n.VerifiedBy,
		})
	}
	return out
}

func edgesToDTO(edges []worldmodel.Edge) []attackGraphEdgeDTO {
	out := make([]attackGraphEdgeDTO, 0, len(edges))
	for _, e := range edges {
		out = append(out, attackGraphEdgeDTO{
			ID:     e.ID,
			Rel:    e.Rel,
			Source: e.Src,
			Target: e.Dst,
			Attrs:  e.Attrs,
		})
	}
	return out
}

func verificationsToDTO(vers []worldmodel.Verification) []attackGraphVerificationDTO {
	out := make([]attackGraphVerificationDTO, 0, len(vers))
	for _, v := range vers {
		out = append(out, attackGraphVerificationDTO{
			ID:         v.ID,
			LeadID:     v.LeadID,
			Primitives: v.Primitives,
			Outcome:    v.Outcome,
			Evidence:   v.Evidence,
			DurationMs: v.DurationMs,
			CreatedAt:  v.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		})
	}
	return out
}
