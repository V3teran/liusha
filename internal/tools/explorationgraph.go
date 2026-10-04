package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/registry"
)

// ─── write_observation ────────────────────────────────────────────────────────

// requestObjectSchema 是自包含 HTTP 请求的 JSON Schema 片段（repro.request/baseline 共用）。
const requestObjectSchema = `{
      "type": "object",
      "description": "完整的自包含 HTTP 请求（完整 URL 含 http(s)://，payload 注入在 url/body 里）",
      "properties": {
        "method":  {"type": "string", "enum": ["GET","POST","PUT","DELETE","PATCH","HEAD","OPTIONS"]},
        "url":     {"type": "string", "description": "完整 URL（含 http:// 或 https://，如 http://host/api?id=payload）"},
        "headers": {"type": "object", "additionalProperties": {"type": "string"}, "description": "请求头（含 Content-Type/Cookie 等，可传 {}）"},
        "body":    {"type": "string", "description": "请求体（GET 通常为空字符串）"}
      },
      "required": ["method", "url", "headers", "body"]
    }`

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
      "description": "自包含复现配方（漏洞假设必填）——域信封 {domain, recipe, assert}。web 域：recipe.request 是你实测过、能触发漏洞特征的完整攻击请求（从 http_request 返回的 request 拷贝改造注入 payload），assert 断言攻击响应独有特征（报错回显/泄露数据/延迟），可选 recipe.baseline 良性对照供机器差分；时间盲用 min_duration_ms。generic 域（非 HTTP：命令序列/多步操作/域渗透）：recipe.steps 自由文本写清执行步骤，assert.description 写坐实判据——评估官将据此自主执行验证。",
      "properties": {
        "domain": {"type": "string", "enum": ["web", "generic"], "description": "复现域，默认 web"},
        "recipe": {
          "oneOf": [
            {
              "type": "object",
              "description": "web 域配方",
              "properties": {
                "request": ` + requestObjectSchema + `,
                "baseline": {
                  "type": "object",
                  "description": "可选：良性对照请求（正常参数版）。提供后机器先放基线再放攻击，断言在基线也命中即拒绝坐实（差分铁律）",
                  "properties": {
                    "method":  {"type": "string", "enum": ["GET","POST","PUT","DELETE","PATCH","HEAD","OPTIONS"]},
                    "url":     {"type": "string", "description": "完整 URL（正常参数）"},
                    "headers": {"type": "object", "additionalProperties": {"type": "string"}},
                    "body":    {"type": "string"}
                  }
                }
              },
              "required": ["request"]
            },
            {
              "type": "object",
              "description": "generic 域配方（非 HTTP 场景）",
              "properties": {
                "steps": {"type": "string", "description": "自由文本执行步骤（命令/工具序列与判定观察点，评估官据此自主执行取证）"}
              },
              "required": ["steps"]
            }
          ]
        },
        "assert": {
          "oneOf": [
            {
              "type": "object",
              "description": "web 域断言：全部满足才算复现成功，至少一条；禁止页面常态断言（status_code:200+登录页标题类）",
              "properties": {
                "status_code":       {"type": "integer", "description": "期望响应码"},
                "body_contains":     {"type": "array", "items": {"type": "string"}, "description": "响应体须含全部子串"},
                "body_not_contains": {"type": "array", "items": {"type": "string"}, "description": "响应体须不含任一子串"},
                "header_contains":   {"type": "object", "description": "响应头 key→子串"},
                "min_duration_ms":   {"type": "integer", "description": "响应耗时至少 N 毫秒——时间盲注入证据（SLEEP(5) 给 4000）"}
              }
            },
            {
              "type": "object",
              "description": "generic 域判据",
              "properties": {
                "description": {"type": "string", "description": "坐实判据的自然语言描述：执行 steps 后观察到什么才算坐实"}
              },
              "required": ["description"]
            }
          ]
        }
      },
      "required": ["recipe", "assert"]
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

	// repro 归一化：分域校验 + 统一成域信封 {domain, recipe, assert} 后存储——
	// 收割层/复现门 thereafter 只认信封，不再感知任何域内形状。
	if len(input.Repro) > 0 {
		normalized, nErr := NormalizeReproEnvelope(input.Repro)
		if nErr != nil {
			return registry.ToolResult{Error: nErr.Error()}, nil
		}
		input.Repro = normalized
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

// NormalizeReproEnvelope 把 LLM 提交的 repro 归一化成域信封 {domain, recipe, assert} 并做分域校验。
// 兼容历史形状：顶层 request/baseline/assert（无 domain）自动包装成 web 域信封。
// 归一化后存储——收割层与复现门只认信封，不感知任何域内形状。
func NormalizeReproEnvelope(repro json.RawMessage) (json.RawMessage, error) {
	var flex struct {
		Domain        string          `json:"domain"`
		Recipe        json.RawMessage `json:"recipe"`
		Request       json.RawMessage `json:"request"`  // 历史 web 形状
		Baseline      json.RawMessage `json:"baseline"` // 历史 web 形状
		Assert        json.RawMessage `json:"assert"`
		TrafficID     *int64          `json:"traffic_id"`    // 旧引用格式（已废弃）
		Modifications json.RawMessage `json:"modifications"` // 旧引用格式（已废弃）
		Steps         string          `json:"steps"`         // 历史 generic 形状
	}
	if err := json.Unmarshal(repro, &flex); err != nil {
		return nil, fmt.Errorf("repro 解析失败: %v", err)
	}
	if flex.TrafficID != nil || len(flex.Modifications) > 0 {
		return nil, fmt.Errorf("❌ 旧格式 repro（traffic_id + modifications）已废弃。\n\n" +
			"✅ 请提供域信封：\n" +
			"{\"domain\": \"web\", \"recipe\": {\"request\": {\"method\",\"url\",\"headers\",\"body\"}}, \"assert\": {...}}\n" +
			"或 generic 域：{\"domain\": \"generic\", \"recipe\": {\"steps\": \"...\"}, \"assert\": {\"description\": \"...\"}}")
	}

	// 无信封的历史形状：顶层 request（web）或 steps（generic）→ 包装成信封。
	domain := flex.Domain
	if domain == "" {
		switch {
		case len(flex.Request) > 0:
			domain = "web"
		case flex.Steps != "":
			domain = "generic"
		}
	}

	var recipe, assert json.RawMessage
	switch domain {
	case "web":
		if len(flex.Recipe) > 0 {
			recipe = flex.Recipe
		} else if len(flex.Request) > 0 {
			// 历史形状包装：request/baseline 提进 recipe。
			wrapped := map[string]json.RawMessage{"request": flex.Request}
			if len(flex.Baseline) > 0 {
				wrapped["baseline"] = flex.Baseline
			}
			b, err := json.Marshal(wrapped)
			if err != nil {
				return nil, fmt.Errorf("repro 归一化失败: %v", err)
			}
			recipe = b
		} else {
			return nil, fmt.Errorf("❌ web 域 repro 缺少 recipe.request。\n\n" +
				"✅ 正确格式：{\"domain\": \"web\", \"recipe\": {\"request\": {\"method\": \"GET\", \"url\": \"http://host/api?id=payload\", \"headers\": {}, \"body\": \"\"}}, \"assert\": {\"body_contains\": [...]}}")
		}
		assert = flex.Assert
		if len(assert) == 0 {
			return nil, fmt.Errorf("❌ repro 缺少 assert 字段。\n\n" +
				"✅ web 域可用字段: status_code, body_contains, body_not_contains, header_contains, min_duration_ms")
		}
		if err := validateWebRequestRecipe(recipe); err != nil {
			return nil, err
		}
	case "generic":
		if len(flex.Recipe) > 0 {
			recipe = flex.Recipe
		} else if flex.Steps != "" {
			b, err := json.Marshal(map[string]string{"steps": flex.Steps})
			if err != nil {
				return nil, fmt.Errorf("repro 归一化失败: %v", err)
			}
			recipe = b
		}
		if len(recipe) == 0 {
			return nil, fmt.Errorf("❌ generic 域 repro 缺少 recipe.steps。\n\n" +
				"✅ 正确格式：{\"domain\": \"generic\", \"recipe\": {\"steps\": \"1. 登录后台 2. 执行 ... 3. 观察 ...\"}, \"assert\": {\"description\": \"看到 X 即坐实\"}}")
		}
		var rc struct {
			Steps string `json:"steps"`
		}
		if err := json.Unmarshal(recipe, &rc); err == nil && strings.TrimSpace(rc.Steps) == "" {
			return nil, fmt.Errorf("❌ generic 域 recipe.steps 不能为空——评估官据此自主执行，无步骤即无可复现")
		}
		assert = flex.Assert
		if len(assert) == 0 {
			return nil, fmt.Errorf("❌ repro 缺少 assert 字段。\n\n" +
				"✅ generic 域判据：{\"description\": \"执行 steps 后观察到什么才算坐实\"}")
		}
		var ac struct {
			Description string `json:"description"`
		}
		if err := json.Unmarshal(assert, &ac); err != nil || strings.TrimSpace(ac.Description) == "" {
			return nil, fmt.Errorf("❌ generic 域 assert.description 不能为空——判据缺失即橡皮图章。\n\n" +
				"✅ 正确格式：{\"description\": \"执行 steps 后观察到 X 且正常路径无法出现，即坐实\"}")
		}
	case "":
		return nil, fmt.Errorf("❌ repro 缺少 domain 信封。\n\n" +
			"✅ web 域：{\"domain\": \"web\", \"recipe\": {\"request\": {\"method\",\"url\",\"headers\",\"body\"}}, \"assert\": {...}}\n" +
			"✅ generic 域（非 HTTP）：{\"domain\": \"generic\", \"recipe\": {\"steps\": \"...\"}, \"assert\": {\"description\": \"...\"}}")
	default:
		return nil, fmt.Errorf("❌ 未知复现域 %q（已支持: web, generic）", domain)
	}

	out, err := json.Marshal(map[string]json.RawMessage{
		"domain": json.RawMessage(`"` + domain + `"`),
		"recipe": recipe,
		"assert": assert,
	})
	if err != nil {
		return nil, fmt.Errorf("repro 归一化失败: %v", err)
	}
	return out, nil
}

// validateWebRequestRecipe 校验 web 域 recipe：request 四字段齐全 + URL 绝对。
func validateWebRequestRecipe(recipe json.RawMessage) error {
	var rc struct {
		Request struct {
			Method  string                 `json:"method"`
			URL     string                 `json:"url"`
			Headers map[string]interface{} `json:"headers"`
			Body    *string                `json:"body"` // 指针区分未提供与空串
		} `json:"request"`
	}
	if err := json.Unmarshal(recipe, &rc); err != nil {
		return fmt.Errorf("❌ web 域 recipe 解析失败: %v", err)
	}
	req := rc.Request
	var missing []string
	if req.Method == "" {
		missing = append(missing, "method")
	}
	if req.URL == "" {
		missing = append(missing, "url")
	}
	if req.Headers == nil {
		missing = append(missing, "headers")
	}
	if req.Body == nil {
		missing = append(missing, "body")
	}
	if len(missing) > 0 {
		return fmt.Errorf("❌ recipe.request 缺少必需字段: %v\n\n"+
			"✅ request 必须四字段齐全（headers 可 {}、body 可 \"\"），url 须含 http:// 或 https://", missing)
	}
	if !strings.HasPrefix(req.URL, "http://") && !strings.HasPrefix(req.URL, "https://") {
		return fmt.Errorf("❌ url 必须是完整 URL（包含 http:// 或 https://），当前 %q", req.URL)
	}
	return nil
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
