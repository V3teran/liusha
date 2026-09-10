package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/V3teran/liusha/internal/registry"
	"github.com/V3teran/liusha/internal/knowledgegraph"
)

// ─── write_observation ────────────────────────────────────────────────────────

var writeObservationSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "statement": {
      "type": "string",
      "description": "假设陈述，如：'目标存在 SQL 注入漏洞'"
    },
    "reasoning": {
      "type": "string",
      "description": "提出假设的理由和推理过程"
    },
    "test_plan": {
      "type": "string",
      "description": "如何验证这个假设的计划"
    },
    "confidence": {
      "type": "string",
      "enum": ["low", "medium", "high"],
      "description": "假设的初始置信度，默认 low"
    }
  },
  "required": ["statement", "reasoning"]
}`)

type writeObservationTool struct{ deps Deps }

func (t *writeObservationTool) Name() string      { return "write_observation" }
func (t *writeObservationTool) ShortDesc() string { return "记录待验证的假设" }
func (t *writeObservationTool) Desc() string {
	return "记录一个待验证的假设到世界模型。假设是基于观察和推理提出的，需要后续通过实验验证。"
}
func (t *writeObservationTool) Schema() json.RawMessage { return writeObservationSchema }

func (t *writeObservationTool) Execute(ctx context.Context, args json.RawMessage) (registry.ToolResult, error) {
	var input struct {
		Statement  string `json:"statement"`
		Reasoning  string `json:"reasoning"`
		TestPlan   string `json:"test_plan"`
		Confidence string `json:"confidence"`
	}
	if err := json.Unmarshal(args, &input); err != nil {
		return registry.ToolResult{Error: "参数解析失败"}, nil
	}

	if input.Statement == "" {
		return registry.ToolResult{Error: "statement 不能为空"}, nil
	}

	// 默认置信度
	if input.Confidence == "" {
		input.Confidence = "low"
	}

	// 构造 content
	content, _ := json.Marshal(map[string]interface{}{
		"statement": input.Statement,
		"reasoning": input.Reasoning,
		"test_plan": input.TestPlan,
	})

	// 映射到 knowledgegraph.Confidence
	var confidence knowledgegraph.Confidence
	switch input.Confidence {
	case "low":
		confidence = "low"
	case "medium":
		confidence = "medium"
	case "high":
		confidence = "high"
	default:
		confidence = "low"
	}

	// 创建节点
	node := knowledgegraph.Node{
		ID:         uuid.New().String(),
		TaskID:     t.deps.TaskID,
		Kind:       knowledgegraph.KindObservation,
		Content:    content,
		Confidence: &confidence,
		Priority:   knowledgegraph.PriorityMedium,
		SourceType: knowledgegraph.SourceExecutor,
		SourceID:   t.deps.AgentID,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}

	// 写入 worldmodel
	id, err := t.deps.World.CreateNode(ctx, node)
	if err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("创建节点失败: %v", err)}, nil
	}

	// 如果有当前 actionID，创建 action → observation 关系（GENERATES）
	if actionID := getContextActionID(ctx); actionID != "" {
		edge := knowledgegraph.Edge{
			TaskID:    t.deps.TaskID,
			SrcID:     actionID,
			Rel:       knowledgegraph.RelGenerates,
			DstID:     id,
			CreatedAt: time.Now(),
		}
		_ = t.deps.World.CreateEdge(ctx, edge)
	}

	return registry.ToolResult{
		Output: fmt.Sprintf("Observation 创建成功\nID: %s\n陈述: %s\n置信度: %s", id, input.Statement, input.Confidence),
	}, nil
}

// ─── write_evidence ──────────────────────────────────────────────────────────

var writeEvidenceSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "observation_id": {
      "type": "string",
      "description": "关联的假设 ID"
    },
    "outcome": {
      "type": "string",
      "enum": ["confirms", "refutes", "inconclusive"],
      "description": "验证结果：confirms（确认）、refutes（反驳）、inconclusive（不确定）"
    },
    "description": {
      "type": "string",
      "description": "证据描述（实验过程、观察结果）"
    },
    "data": {
      "type": "object",
      "description": "原始数据（payload、响应、日志等）"
    },
    "finding_id": {
      "type": "string",
      "description": "如果 outcome=confirms，关联的 finding ID（可选）"
    }
  },
  "required": ["observation_id", "outcome", "description"]
}`)

type writeEvidenceTool struct{ deps Deps }

func (t *writeEvidenceTool) Name() string      { return "write_evidence" }
func (t *writeEvidenceTool) ShortDesc() string { return "记录验证证据" }
func (t *writeEvidenceTool) Desc() string {
	return "记录验证假设的证据。证据可以确认（confirms）或反驳（refutes）假设，或者结果不确定（inconclusive）。"
}
func (t *writeEvidenceTool) Schema() json.RawMessage { return writeEvidenceSchema }

func (t *writeEvidenceTool) Execute(ctx context.Context, args json.RawMessage) (registry.ToolResult, error) {
	var input struct {
		ObservationID string                 `json:"observation_id"`
		Outcome      string                 `json:"outcome"`
		Description  string                 `json:"description"`
		Data         map[string]interface{} `json:"data"`
		FindingID    string                 `json:"finding_id"`
	}
	if err := json.Unmarshal(args, &input); err != nil {
		return registry.ToolResult{Error: "参数解析失败"}, nil
	}

	if input.ObservationID == "" || input.Outcome == "" || input.Description == "" {
		return registry.ToolResult{Error: "observation_id, outcome, description 不能为空"}, nil
	}

	// 验证 outcome
	if input.Outcome != "confirms" && input.Outcome != "refutes" && input.Outcome != "inconclusive" {
		return registry.ToolResult{Error: "outcome 必须是 confirms/refutes/inconclusive"}, nil
	}

	// 构造 content
	content, _ := json.Marshal(map[string]interface{}{
		"outcome":     input.Outcome,
		"description": input.Description,
		"data":        input.Data,
	})

	// 创建 evidence 节点
	node := knowledgegraph.Node{
		ID:         uuid.New().String(),
		TaskID:     t.deps.TaskID,
		Kind:       knowledgegraph.KindEvaluation,
		Content:    content,
		Priority:   knowledgegraph.PriorityMedium,
		SourceType: knowledgegraph.SourceExecutor,
		SourceID:   t.deps.AgentID,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}

	id, err := t.deps.World.CreateNode(ctx, node)
	if err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("创建节点失败: %v", err)}, nil
	}

	// 创建关系边
	edges := []knowledgegraph.Edge{}

	// 1. action → evidence (GENERATES)
	if actionID := getContextActionID(ctx); actionID != "" {
		edges = append(edges, knowledgegraph.Edge{
			TaskID:    t.deps.TaskID,
			SrcID:     actionID,
			Rel:       knowledgegraph.RelGenerates,
			DstID:     id,
			CreatedAt: time.Now(),
		})
	}

	// 2. evidence → observation (CONFIRMS 或 REFUTES)
	if input.Outcome == "confirms" {
		edges = append(edges, knowledgegraph.Edge{
			TaskID:    t.deps.TaskID,
			SrcID:     id,
			Rel:       knowledgegraph.RelConfirms,
			DstID:     input.ObservationID,
			CreatedAt: time.Now(),
		})

		// 更新 observation 的置信度为 verified
		verified := knowledgegraph.Confidence("verified")
		_ = t.deps.World.UpdateNodeConfidence(ctx, input.ObservationID, verified)

		// 3. 如果有 finding_id，创建 evidence → finding (CONFIRMS)
		if input.FindingID != "" {
			edges = append(edges, knowledgegraph.Edge{
				TaskID:    t.deps.TaskID,
				SrcID:     id,
				Rel:       knowledgegraph.RelConfirms,
				DstID:     input.FindingID,
				CreatedAt: time.Now(),
			})
		}
	} else if input.Outcome == "refutes" {
		edges = append(edges, knowledgegraph.Edge{
			TaskID:    t.deps.TaskID,
			SrcID:     id,
			Rel:       knowledgegraph.RelRefutes,
			DstID:     input.ObservationID,
			CreatedAt: time.Now(),
		})

		// 更新 observation 的置信度为 low
		low := knowledgegraph.Confidence("low")
		_ = t.deps.World.UpdateNodeConfidence(ctx, input.ObservationID, low)
	}

	// 创建所有边
	for _, edge := range edges {
		_ = t.deps.World.CreateEdge(ctx, edge)
	}

	return registry.ToolResult{
		Output: fmt.Sprintf("Evidence 创建成功\nID: %s\nOutcome: %s\n关联 observation: %s", id, input.Outcome, input.ObservationID),
	}, nil
}

// getContextActionID 从 context 中获取当前 actionID（如果有）
func getContextActionID(ctx context.Context) string {
	if actionID, ok := ctx.Value("current_action_id").(string); ok {
		return actionID
	}
	return ""
}
