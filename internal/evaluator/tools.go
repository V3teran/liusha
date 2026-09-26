package evaluator

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/traffic"
	"github.com/google/uuid"
)

// ============================================
// GetObservationTool - 获取 observation 详情
// ============================================

type GetObservationTool struct {
	world *explorationgraph.Store
}

func NewGetObservationTool(world *explorationgraph.Store) *GetObservationTool {
	return &GetObservationTool{world: world}
}

func (t *GetObservationTool) Name() string {
	return "get_observation"
}

func (t *GetObservationTool) Description() string {
	return "获取 observation 的完整信息"
}

func (t *GetObservationTool) Schema() core.ToolSchema {
	inputSchema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"observation_id": {
				"type": "string",
				"description": "Observation 节点 ID"
			}
		},
		"required": ["observation_id"]
	}`)

	return core.ToolSchema{
		InputSchema: inputSchema,
	}
}

func (t *GetObservationTool) Execute(ctx context.Context, input core.ToolInput) (core.ToolOutput, error) {
	var args struct {
		ObservationID string `json:"observation_id"`
	}
	if err := json.Unmarshal(input.Arguments, &args); err != nil {
		return core.ToolOutput{Error: fmt.Sprintf("invalid arguments: %v", err)}, nil
	}

	node, err := t.world.GetNode(ctx, args.ObservationID)
	if err != nil {
		return core.ToolOutput{Error: fmt.Sprintf("get node: %v", err)}, nil
	}

	result, _ := json.Marshal(node)
	return core.ToolOutput{Result: result}, nil
}

// ============================================
// GetTrafficTool - 获取相关流量数据
// ============================================

type GetTrafficTool struct {
	traffic *traffic.AgentStore
}

func NewGetTrafficTool(trafficStore *traffic.AgentStore) *GetTrafficTool {
	return &GetTrafficTool{traffic: trafficStore}
}

func (t *GetTrafficTool) Name() string {
	return "get_traffic"
}

func (t *GetTrafficTool) Description() string {
	return "获取相关的 HTTP 流量数据（用于漏洞验证）"
}

func (t *GetTrafficTool) Schema() core.ToolSchema {
	inputSchema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"url": {
				"type": "string",
				"description": "URL 关键词（可选）"
			},
			"limit": {
				"type": "integer",
				"description": "返回数量限制，默认 10",
				"default": 10
			}
		},
		"required": []
	}`)

	return core.ToolSchema{
		InputSchema: inputSchema,
	}
}

func (t *GetTrafficTool) Execute(ctx context.Context, input core.ToolInput) (core.ToolOutput, error) {
	var args struct {
		URL   string `json:"url"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(input.Arguments, &args); err != nil {
		return core.ToolOutput{Error: fmt.Sprintf("invalid arguments: %v", err)}, nil
	}

	if args.Limit == 0 {
		args.Limit = 10
	}

	// TODO: 实现流量查询逻辑
	// 当前返回空结果
	result := json.RawMessage(`{"traffic": [], "message": "traffic query not implemented yet"}`)
	return core.ToolOutput{Result: result}, nil
}

// ============================================
// CreateFindingTool - 创建漏洞 finding
// ============================================

type CreateFindingTool struct {
	findingStore *finding.Store
	world        *explorationgraph.Store
}

func NewCreateFindingTool(findingStore *finding.Store, world *explorationgraph.Store) *CreateFindingTool {
	return &CreateFindingTool{
		findingStore: findingStore,
		world:        world,
	}
}

func (t *CreateFindingTool) Name() string {
	return "create_finding"
}

func (t *CreateFindingTool) Description() string {
	return "创建一个漏洞 finding（确认漏洞后调用）"
}

func (t *CreateFindingTool) Schema() core.ToolSchema {
	inputSchema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"observation_id": {
				"type": "string",
				"description": "对应的 observation ID"
			},
			"task_id": {
				"type": "string",
				"description": "任务 ID"
			},
			"severity": {
				"type": "string",
				"description": "严重性等级",
				"enum": ["critical", "high", "medium", "low", "info"]
			},
			"summary": {
				"type": "string",
				"description": "漏洞摘要"
			},
			"evaluation": {
				"type": "string",
				"description": "验证过程和证据"
			}
		},
		"required": ["observation_id", "task_id", "severity", "summary"]
	}`)

	return core.ToolSchema{
		InputSchema: inputSchema,
	}
}

func (t *CreateFindingTool) Execute(ctx context.Context, input core.ToolInput) (core.ToolOutput, error) {
	var args struct {
		ObservationID string `json:"observation_id"`
		TaskID        string `json:"task_id"`
		Severity      string `json:"severity"`
		Summary       string `json:"summary"`
		Evaluation    string `json:"evaluation"`
	}
	if err := json.Unmarshal(input.Arguments, &args); err != nil {
		return core.ToolOutput{Error: fmt.Sprintf("invalid arguments: %v", err)}, nil
	}

	// 1. 创建 finding 记录
	f := finding.VulnFinding{
		ID:          uuid.New().String(),
		TaskID:      args.TaskID,
		Severity:    args.Severity,
		Summary:     args.Summary,
		Evaluation:  json.RawMessage(fmt.Sprintf(`{"details":"%s"}`, args.Evaluation)),
		FirstSeenAt: time.Now(),
	}

	saved, err := t.findingStore.Save(ctx, f)
	if err != nil {
		return core.ToolOutput{Error: fmt.Sprintf("save finding: %v", err)}, nil
	}

	// 2. 晋升到 WorldModel
	findingNodeID := uuid.New().String()
	verified := explorationgraph.ConfidenceVerified

	findingNode := explorationgraph.Node{
		ID:         findingNodeID,
		TaskID:     args.TaskID,
		Kind:       core.KindResult,
		Content:    json.RawMessage(fmt.Sprintf(`{"finding_id":"%s","type":"vulnerability"}`, saved.ID)),
		Confidence: &verified,
		SourceType: explorationgraph.SourceEvaluator,
		SourceID:   args.ObservationID,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}

	_, err = t.world.CreateNode(ctx, findingNode)
	if err != nil {
		return core.ToolOutput{Error: fmt.Sprintf("create node in knowledge graph: %v", err)}, nil
	}

	// 3. 返回结果
	result := map[string]interface{}{
		"finding_id":      saved.ID,
		"finding_node_id": findingNodeID,
		"severity":        saved.Severity,
		"created_at":      saved.FirstSeenAt.Format(time.RFC3339),
	}

	resultJSON, _ := json.Marshal(result)
	return core.ToolOutput{Result: resultJSON}, nil
}
