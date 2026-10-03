package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/registry"
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
    },
    "severity": {
      "type": "string",
      "enum": ["critical", "high", "medium", "low"],
      "description": "若坐实，漏洞严重度（可选，默认 medium）"
    },
    "repro": {
      "type": "object",
      "description": "差分复现配方（漏洞假设必填）。铁律：modifications 必须注入 payload/改写构造攻击请求（原样重放会被拒绝——命中只能证明页面正常）；断言必须捕捉攻击响应独有特征（报错回显/泄露数据/延迟），禁止页面常态断言（status_code=200+登录页标题类）。traffic_id 引用良性原始流量，时间盲注入断言用 min_duration_ms。",
      "properties": {
        "traffic_id": {"type": "integer", "description": "要重放的源流量 ID（http_request 的返回值或 list_traffic 查到的 id）"},
        "modifications": {
          "type": "object",
          "description": "必填。payload 注入点（只写要改的字段，其余继承原请求）",
          "properties": {
            "body_fields": {"type": "object", "description": "表单字段改写：{\"id\": \"1' UNION SELECT 1,2,3--\"}", "additionalProperties": {"type": "string"}},
            "body":       {"type": "string", "description": "整体请求体替换"},
            "query":      {"type": "object", "description": "查询参数改写：{\"q\": \"payload\"}", "additionalProperties": {"type": "string"}},
            "headers":    {"type": "object", "description": "请求头改写：{\"Cookie\": \"...\"}", "additionalProperties": {"type": "string"}},
            "method":     {"type": "string"},
            "url":        {"type": "string"}
          }
        },
        "assert": {
          "type": "object",
          "description": "坐实断言：全部满足才算复现成功，至少一条",
          "properties": {
            "status_code":     {"type": "integer", "description": "期望响应码"},
            "body_contains":   {"type": "array", "items": {"type": "string"}, "description": "响应体须含全部子串"},
            "body_absent":     {"type": "array", "items": {"type": "string"}, "description": "响应体须不含任一子串"},
            "header_contains": {"type": "object", "description": "响应头 key→子串"},
            "min_duration_ms": {"type": "integer", "description": "响应耗时至少 N 毫秒——时间盲注入证据（SLEEP(5) 给 4000）"}
          }
        }
      },
      "required": ["traffic_id", "modifications", "assert"]
    }
  },
  "required": ["statement", "reasoning"]
}`)

type writeObservationTool struct {
	registry.BaseTool
	deps Deps
}

func newWriteObservationTool(deps Deps, timeout time.Duration, safe bool) *writeObservationTool {
	t := &writeObservationTool{deps: deps}
	t.SetTimeout(timeout)
	t.SetConcurrencySafe(safe)
	return t
}

func (t *writeObservationTool) Name() string      { return "write_observation" }
func (t *writeObservationTool) ShortDesc() string { return "记录待验证的假设" }
func (t *writeObservationTool) Desc() string {
	return "记录一个待验证的假设到探索图。假设是基于观察和推理提出的，需要后续通过实验验证。"
}
func (t *writeObservationTool) Schema() json.RawMessage { return writeObservationSchema }

func (t *writeObservationTool) Execute(ctx context.Context, args json.RawMessage) (registry.ToolResult, error) {
	var input struct {
		Statement  string          `json:"statement"`
		Reasoning  string          `json:"reasoning"`
		TestPlan   string          `json:"test_plan"`
		Confidence string          `json:"confidence"`
		Severity   string          `json:"severity"`
		Repro      json.RawMessage `json:"repro"`
	}
	if err := json.Unmarshal(args, &input); err != nil {
		return registry.ToolResult{Error: "参数解析失败"}, nil
	}

	if input.Statement == "" {
		return registry.ToolResult{Error: "statement 不能为空"}, nil
	}

	// 验证 repro 格式（强制使用新的自包含格式）
	if len(input.Repro) > 0 {
		var reproCheck struct {
			Request       map[string]interface{} `json:"request"`
			TrafficID     *int64                 `json:"traffic_id"`
			Modifications map[string]interface{} `json:"modifications"`
		}
		if err := json.Unmarshal(input.Repro, &reproCheck); err == nil {
			// 检查是否使用了旧格式
			if reproCheck.TrafficID != nil || len(reproCheck.Modifications) > 0 {
				return registry.ToolResult{
					Error: "repro 格式已更新。请使用新格式：repro.request (包含完整的 method/url/headers/body)。\n" +
						"示例：{\"request\": {\"method\": \"GET\", \"url\": \"http://target.com/...\", \"headers\": {}, \"body\": \"\"}, \"assert\": {...}}\n" +
						"旧的 traffic_id + modifications 模式不再支持。请参考文档中的完整示例。",
				}, nil
			}
			// 检查是否提供了新格式的 request
			if len(reproCheck.Request) == 0 {
				return registry.ToolResult{
					Error: "repro 必须包含 request 字段（完整的 HTTP 请求）。\n" +
						"request 必须包含：method, url, headers, body。\n" +
						"示例：{\"method\": \"GET\", \"url\": \"http://target.com/api?id=1\", \"headers\": {}, \"body\": \"\"}",
				}, nil
			}
		}
	}

	// 默认置信度
	if input.Confidence == "" {
		input.Confidence = "low"
	}

	// 构造 content（repro/severity 透传——executor agent 收割后交复现门裁决）
	contentMap := map[string]interface{}{
		"statement": input.Statement,
		"reasoning": input.Reasoning,
		"test_plan": input.TestPlan,
	}
	if input.Severity != "" {
		contentMap["severity"] = input.Severity
	}
	if len(input.Repro) > 0 {
		contentMap["repro"] = input.Repro
	}
	content, _ := json.Marshal(contentMap)

	// 映射到 explorationgraph.Confidence
	var confidence explorationgraph.Confidence
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
	node := explorationgraph.Node{
		ID:         uuid.New().String(),
		TaskID:     t.deps.TaskID,
		Kind:       core.KindObservation,
		Content:    content,
		Confidence: &confidence,
		Priority:   explorationgraph.PriorityMedium,
		SourceType: explorationgraph.SourceExecutor,
		SourceID:   t.deps.AgentID,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}

	// 写入探索图
	id, err := t.deps.Graph.CreateNode(ctx, node)
	if err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("创建节点失败: %v", err)}, nil
	}

	// 如果有当前 actionID，创建 action → observation 关系（GENERATES）
	if actionID := getContextActionID(ctx); actionID != "" {
		edge := explorationgraph.Edge{
			TaskID:    t.deps.TaskID,
			SrcID:     actionID,
			Rel:       explorationgraph.RelGenerates,
			DstID:     id,
			CreatedAt: time.Now(),
		}
		if err := t.deps.Graph.CreateBusinessEdge(ctx, edge); err != nil {
			// 边创建失败不中断主流程
			_ = err
		}
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

type writeEvidenceTool struct {
	registry.BaseTool
	deps Deps
}

func newWriteEvidenceTool(deps Deps, timeout time.Duration, safe bool) *writeEvidenceTool {
	t := &writeEvidenceTool{deps: deps}
	t.SetTimeout(timeout)
	t.SetConcurrencySafe(safe)
	return t
}

func (t *writeEvidenceTool) Name() string      { return "write_evidence" }
func (t *writeEvidenceTool) ShortDesc() string { return "记录验证证据" }
func (t *writeEvidenceTool) Desc() string {
	return "记录验证假设的证据。证据可以确认（confirms）或反驳（refutes）假设，或者结果不确定（inconclusive）。"
}
func (t *writeEvidenceTool) Schema() json.RawMessage { return writeEvidenceSchema }

func (t *writeEvidenceTool) Execute(ctx context.Context, args json.RawMessage) (registry.ToolResult, error) {
	var input struct {
		ObservationID string                 `json:"observation_id"`
		Outcome       string                 `json:"outcome"`
		Description   string                 `json:"description"`
		Data          map[string]interface{} `json:"data"`
		FindingID     string                 `json:"finding_id"`
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
		"type":      "evidence",
		"statement": input.Description,
		"outcome":   input.Outcome,
		"data":      input.Data,
	})

	// 创建 evidence 节点（evidence 是一类 observation，evaluation 节点已废弃）
	node := explorationgraph.Node{
		ID:         uuid.New().String(),
		TaskID:     t.deps.TaskID,
		Kind:       core.KindObservation,
		Content:    content,
		Priority:   explorationgraph.PriorityMedium,
		SourceType: explorationgraph.SourceExecutor,
		SourceID:   t.deps.AgentID,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}

	id, err := t.deps.Graph.CreateNode(ctx, node)
	if err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("创建节点失败: %v", err)}, nil
	}

	// 创建关系边
	edges := []explorationgraph.Edge{}

	// 1. action → evidence (GENERATES)
	if actionID := getContextActionID(ctx); actionID != "" {
		edges = append(edges, explorationgraph.Edge{
			TaskID:    t.deps.TaskID,
			SrcID:     actionID,
			Rel:       explorationgraph.RelGenerates,
			DstID:     id,
			CreatedAt: time.Now(),
		})
	}

	// 2. evidence → observation (CONFIRMS 或 REFUTES)
	switch input.Outcome {
	case "confirms":
		edges = append(edges, explorationgraph.Edge{
			TaskID:    t.deps.TaskID,
			SrcID:     id,
			Rel:       explorationgraph.RelConfirms,
			DstID:     input.ObservationID,
			CreatedAt: time.Now(),
		})

		// 更新 observation 的置信度为 verified
		verified := explorationgraph.Confidence("verified")
		if err := t.deps.Graph.UpdateNodeConfidence(ctx, input.ObservationID, verified); err != nil {
			// 置信度更新失败不中断主流程
			_ = err
		}

		// 3. 如果有 finding_id，创建 evidence → finding (CONFIRMS)
		if input.FindingID != "" {
			edges = append(edges, explorationgraph.Edge{
				TaskID:    t.deps.TaskID,
				SrcID:     id,
				Rel:       explorationgraph.RelConfirms,
				DstID:     input.FindingID,
				CreatedAt: time.Now(),
			})
		}
	case "refutes":
		edges = append(edges, explorationgraph.Edge{
			TaskID:    t.deps.TaskID,
			SrcID:     id,
			Rel:       explorationgraph.RelRefutes,
			DstID:     input.ObservationID,
			CreatedAt: time.Now(),
		})

		// 更新 observation 的置信度为 low
		low := explorationgraph.Confidence("low")
		if err := t.deps.Graph.UpdateNodeConfidence(ctx, input.ObservationID, low); err != nil {
			// 置信度更新失败不中断主流程
			_ = err
		}
	}

	// 创建所有边
	for _, edge := range edges {
		if err := t.deps.Graph.CreateBusinessEdge(ctx, edge); err != nil {
			// 边创建失败不中断主流程
			_ = err
		}
	}

	return registry.ToolResult{
		Output: fmt.Sprintf("Evidence 创建成功\nID: %s\nOutcome: %s\n关联 observation: %s", id, input.Outcome, input.ObservationID),
	}, nil
}

// actionCtxKey 与 getContextActionID 共用同一 context 键（string key，历史口径）。
const actionCtxKey = "current_action_id"

// WithActionContext 把当前执行的 Action ID 挂进 ctx——engine 在把 ctx 交给 ReAct
// runtime 前调用，write_observation/write_evidence 据此建 action→节点 的归属边。
func WithActionContext(ctx context.Context, actionID string) context.Context {
	return context.WithValue(ctx, actionCtxKey, actionID) //nolint:staticcheck // 历史键口径，getter 同源
}

// getContextActionID 从 context 中获取当前 actionID（如果有）
func getContextActionID(ctx context.Context) string {
	if actionID, ok := ctx.Value(actionCtxKey).(string); ok {
		return actionID
	}
	return ""
}
