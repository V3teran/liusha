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
      "description": "自包含复现配方（漏洞假设必填）——域信封 {domain, recipe, assert}，不依赖任何工具或流量库。web 域（HTTP 发现的漏洞）：recipe.request 是实测过、能触发漏洞特征的完整攻击请求（从 http_request 返回的 request 拷贝改造注入 payload），assert 断言攻击响应独有特征（报错回显/泄露数据/延迟），可选 recipe.baseline 良性对照供机器差分；时间盲用 min_duration_ms。generic 域（浏览器发现的漏洞如 DOM XSS、非 HTTP 场景如命令/多步操作/域渗透）：recipe.steps 自由文本写清执行步骤与判定观察点，assert.description 写坐实判据——评估官据此自主执行验证。",
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

func (t *writeObservationTool) Name() string { return "write_observation" }
func (t *writeObservationTool) ShortDesc() string {
	return "⚠️【必须】报告发现的漏洞假设（附复现配方repro），未报告=无效发现"
}
func (t *writeObservationTool) Desc() string {
	return "记录一个待验证的漏洞假设到探索图。必须包含自包含的复现配方（repro字段），evaluator会据此自主验证。未通过此工具报告的发现不会进入最终报告。"
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
	// 收割层/复现门从此只认信封，不再感知任何域内形状。
	normalizeNote := ""
	if len(input.Repro) > 0 {
		normalized, note, nErr := NormalizeReproEnvelope(input.Repro)
		if nErr != nil {
			return registry.ToolResult{Error: nErr.Error()}, nil
		}
		input.Repro = normalized
		normalizeNote = note
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
		SourceID:   t.deps.AgentRunID,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}

	// 写入探索图
	id, err := t.deps.Graph.CreateNode(ctx, node)
	if err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("创建节点失败: %v", err)}, nil
	}

	// 如果有当前 actionID，创建 action → observation 关系（GENERATES）。
	// 边失败不中断节点创建（节点是主体），但要在输出中可见——静默吞掉会丢溯源。
	warnings := ""
	if actionID := getContextActionID(ctx); actionID != "" {
		edge := explorationgraph.Edge{
			TaskID:    t.deps.TaskID,
			SrcID:     actionID,
			Rel:       explorationgraph.RelGenerates,
			DstID:     id,
			CreatedAt: time.Now(),
		}
		if err := t.deps.Graph.CreateBusinessEdge(ctx, edge); err != nil {
			warnings += fmt.Sprintf("\n⚠️ 归属边创建失败（action=%s）: %v", actionID, err)
		}
	}

	return registry.ToolResult{
		Output: fmt.Sprintf("Observation 创建成功\nID: %s\n陈述: %s\n置信度: %s%s%s", id, input.Statement, input.Confidence, normalizeNote, warnings),
	}, nil
}

// NormalizeReproEnvelope 把 LLM 提交的 repro 归一化成域信封 {domain, recipe, assert} 并做分域校验。
// 兼容历史形状：顶层 request/baseline/assert（无 domain）自动包装成 web 域信封；
// assert 误放进 recipe 时自动提升（返回的教学提示随工具输出回给 LLM）。
// 归一化后存储——收割层与复现门只认信封，不感知任何域内形状。
// 返回 (信封, 教学提示（可空）, 错误)。
func NormalizeReproEnvelope(repro json.RawMessage) (json.RawMessage, string, error) {
	flex, err := parseReproEnvelope(repro)
	if err != nil {
		return nil, "", err
	}
	note, err := promoteAssertFromRecipe(&flex)
	if err != nil {
		return nil, "", err
	}

	domain := flex.Domain
	if domain == "" {
		domain = sniffReproDomain(flex)
	}

	var recipe, assert json.RawMessage
	switch domain {
	case "web":
		recipe, assert, err = normalizeWebRepro(flex)
	case "generic":
		recipe, assert, err = normalizeGenericRepro(flex)
	case "":
		return nil, "", fmt.Errorf("❌ repro 缺少 domain 信封。\n\n" +
			"✅ web 域：{\"domain\": \"web\", \"recipe\": {\"request\": {\"method\",\"url\",\"headers\",\"body\"}}, \"assert\": {...}}\n" +
			"✅ generic 域（非 HTTP）：{\"domain\": \"generic\", \"recipe\": {\"steps\": \"...\"}, \"assert\": {\"description\": \"...\"}}")
	default:
		return nil, "", fmt.Errorf("❌ 未知复现域 %q（已支持: web, generic）", domain)
	}
	if err != nil {
		return nil, "", err
	}

	out, err := json.Marshal(map[string]json.RawMessage{
		"domain": json.RawMessage(`"` + domain + `"`),
		"recipe": recipe,
		"assert": assert,
	})
	if err != nil {
		return nil, "", fmt.Errorf("repro 归一化失败: %v", err)
	}
	return out, note, nil
}

// reproEnvelope 是 repro 信封的自由形状（兼容历史字段，归一化前嗅探）。
type reproEnvelope struct {
	Domain        string          `json:"domain"`
	Recipe        json.RawMessage `json:"recipe"`
	Request       json.RawMessage `json:"request"`  // 历史 web 形状
	Baseline      json.RawMessage `json:"baseline"` // 历史 web 形状
	Assert        json.RawMessage `json:"assert"`
	TrafficID     *int64          `json:"traffic_id"`    // 旧引用格式（已废弃）
	Modifications json.RawMessage `json:"modifications"` // 旧引用格式（已废弃）
	Steps         string          `json:"steps"`         // 历史 generic 形状
}

// parseReproEnvelope 解析信封并拒绝已废弃的旧引用格式。
func parseReproEnvelope(repro json.RawMessage) (reproEnvelope, error) {
	var flex reproEnvelope
	if err := json.Unmarshal(repro, &flex); err != nil {
		return flex, fmt.Errorf("repro 解析失败: %v", err)
	}
	if flex.TrafficID != nil || len(flex.Modifications) > 0 {
		return flex, fmt.Errorf("❌ 旧格式 repro（traffic_id + modifications）已废弃。\n\n" +
			"✅ 请提供域信封：\n" +
			"{\"domain\": \"web\", \"recipe\": {\"request\": {\"method\",\"url\",\"headers\",\"body\"}}, \"assert\": {...}}\n" +
			"或 generic 域：{\"domain\": \"generic\", \"recipe\": {\"steps\": \"...\"}, \"assert\": {\"description\": \"...\"}}")
	}
	return flex, nil
}

// promoteAssertFromRecipe 处理 assert 误放进 recipe 的常见笔误（e2e 实测 12 连犯）：
// 自动提升到顶层并继续，返回教学提示让 LLM 下次写对位置。
func promoteAssertFromRecipe(flex *reproEnvelope) (string, error) {
	var recipeProbe struct {
		Assert json.RawMessage `json:"assert"`
	}
	if len(flex.Assert) > 0 || len(flex.Recipe) == 0 ||
		json.Unmarshal(flex.Recipe, &recipeProbe) != nil || len(recipeProbe.Assert) == 0 {
		return "", nil
	}
	flex.Assert = recipeProbe.Assert
	// 从 recipe 中剔除已提升的 assert，避免双份。
	var rm map[string]json.RawMessage
	if json.Unmarshal(flex.Recipe, &rm) == nil {
		delete(rm, "assert")
		if b, mErr := json.Marshal(rm); mErr == nil {
			flex.Recipe = b
		}
	}
	return "\n\nℹ️ 注意：assert 应写在 repro 顶层（与 domain/recipe 平级），本次已自动从 recipe 中提升——下次请直接写对位置。", nil
}

// sniffReproDomain 对漏写 domain 的历史形状做嗅探：顶层/recipe 内 request → web，
// steps → generic（LLM 常见笔误）。
func sniffReproDomain(flex reproEnvelope) string {
	var probe struct {
		Steps   string          `json:"steps"`
		Request json.RawMessage `json:"request"`
	}
	_ = json.Unmarshal(flex.Recipe, &probe)
	switch {
	case len(flex.Request) > 0 || len(probe.Request) > 0:
		return "web"
	case flex.Steps != "" || probe.Steps != "":
		return "generic"
	default:
		return ""
	}
}

// normalizeWebRepro 归一化 web 域 repro：历史形状包装 + assert/request 校验。
func normalizeWebRepro(flex reproEnvelope) (recipe, assert json.RawMessage, err error) {
	switch {
	case len(flex.Recipe) > 0:
		recipe = flex.Recipe
	case len(flex.Request) > 0:
		// 历史形状包装：request/baseline 提进 recipe。
		wrapped := map[string]json.RawMessage{"request": flex.Request}
		if len(flex.Baseline) > 0 {
			wrapped["baseline"] = flex.Baseline
		}
		b, mErr := json.Marshal(wrapped)
		if mErr != nil {
			return nil, nil, fmt.Errorf("repro 归一化失败: %v", mErr)
		}
		recipe = b
	default:
		return nil, nil, fmt.Errorf("❌ web 域 repro 缺少 recipe.request。\n\n" +
			"✅ 正确格式：{\"domain\": \"web\", \"recipe\": {\"request\": {\"method\": \"GET\", \"url\": \"http://host/api?id=payload\", \"headers\": {}, \"body\": \"\"}}, \"assert\": {\"body_contains\": [...]}}")
	}
	assert = flex.Assert
	if len(assert) == 0 {
		return nil, nil, fmt.Errorf("❌ repro 缺少 assert 字段。\n\n" +
			"✅ web 域可用字段: status_code, body_contains, body_not_contains, header_contains, min_duration_ms")
	}
	if err := validateWebRequestRecipe(recipe); err != nil {
		return nil, nil, err
	}
	if err := teachWebAssertDiscriminative(recipe, assert); err != nil {
		return nil, nil, err
	}
	return recipe, assert, nil
}

// normalizeGenericRepro 归一化 generic 域 repro：steps 非空 + assert.description 判据强制。
func normalizeGenericRepro(flex reproEnvelope) (recipe, assert json.RawMessage, err error) {
	switch {
	case len(flex.Recipe) > 0:
		recipe = flex.Recipe
	case flex.Steps != "":
		b, mErr := json.Marshal(map[string]string{"steps": flex.Steps})
		if mErr != nil {
			return nil, nil, fmt.Errorf("repro 归一化失败: %v", mErr)
		}
		recipe = b
	}
	if len(recipe) == 0 {
		return nil, nil, fmt.Errorf("❌ generic 域 repro 缺少 recipe.steps。\n\n" +
			"✅ 正确格式：{\"domain\": \"generic\", \"recipe\": {\"steps\": \"1. 登录后台 2. 执行 ... 3. 观察 ...\"}, \"assert\": {\"description\": \"看到 X 即坐实\"}}")
	}
	var rc struct {
		Steps string `json:"steps"`
	}
	if err := json.Unmarshal(recipe, &rc); err == nil && strings.TrimSpace(rc.Steps) == "" {
		return nil, nil, fmt.Errorf("❌ generic 域 recipe.steps 不能为空——评估官据此自主执行，无步骤即无可复现")
	}
	assert = flex.Assert
	if len(assert) == 0 {
		return nil, nil, fmt.Errorf("❌ repro 缺少 assert 字段。\n\n" +
			"✅ generic 域判据：{\"description\": \"执行 steps 后观察到什么才算坐实\"}")
	}
	var ac struct {
		Description string `json:"description"`
	}
	if err := json.Unmarshal(assert, &ac); err != nil || strings.TrimSpace(ac.Description) == "" {
		return nil, nil, fmt.Errorf("❌ generic 域 assert.description 不能为空——判据缺失即橡皮图章。\n\n" +
			"✅ 正确格式：{\"description\": \"执行 steps 后观察到 X 且正常路径无法出现，即坐实\"}")
	}
	return recipe, assert, nil
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

// teachWebAssertDiscriminative 断言鉴别力的早期教学（与复现门 assertDiscriminative
// 同一策略口径；门是权威防线，这里只求 LLM 早一步拿到可行动反馈省迭代）。
func teachWebAssertDiscriminative(recipe, assert json.RawMessage) error {
	var rc struct {
		Baseline json.RawMessage `json:"baseline"`
	}
	_ = json.Unmarshal(recipe, &rc)
	hasBaseline := len(rc.Baseline) > 0

	var ac struct {
		StatusCode    *int              `json:"status_code"`
		BodyContains  []string          `json:"body_contains"`
		HeaderContain map[string]string `json:"header_contains"`
		MinDurationMs *int              `json:"min_duration_ms"`
	}
	if err := json.Unmarshal(assert, &ac); err != nil {
		return nil // 结构留给闸门裁决，写入侧不做硬失败
	}
	for _, want := range ac.BodyContains {
		if len(strings.TrimSpace(want)) < 4 {
			return fmt.Errorf("❌ 无鉴别力断言——body_contains 子串 %q 过短（几乎必命中任意页面）。\n\n"+
				"✅ 断言应捕捉攻击响应独有特征：报错回显/泄露数据的完整子串（如 \"SQL syntax error\"、dump 出的字段值）", want)
		}
	}
	if hasBaseline {
		return nil
	}
	strong := len(ac.BodyContains) > 0 || len(ac.HeaderContain) > 0 || ac.MinDurationMs != nil
	if !strong && ac.StatusCode != nil && *ac.StatusCode != 200 && *ac.StatusCode != 301 && *ac.StatusCode != 302 && *ac.StatusCode != 304 {
		strong = true
	}
	if !strong {
		return fmt.Errorf("❌ 无鉴别力断言——无 baseline 时至少一个强谓词（body_contains/header_contains/min_duration_ms 或非 200/301/302/304 状态码）。\n\n" +
			"✅ 裸 status_code=200 类页面常态断言会被复现门拒绝坐实；建议附 recipe.baseline 良性对照")
	}
	return nil
}

// ─── write_evidence ──────────────────────────────────────────────────────────

// actionCtxKey 是当前执行 Action ID 的类型化 context 键（SA1029：禁止裸 string 键）。
type actionCtxKey string

const ctxActionID actionCtxKey = "current_action_id"

// WithActionContext 把当前执行的 Action ID 挂进 ctx——engine 在把 ctx 交给 ReAct
// runtime 前调用，write_observation/write_evidence 据此建 action→节点 的归属边。
func WithActionContext(ctx context.Context, actionID string) context.Context {
	return context.WithValue(ctx, ctxActionID, actionID)
}

// getContextActionID 从 context 中获取当前 actionID（如果有）
func getContextActionID(ctx context.Context) string {
	if actionID, ok := ctx.Value(ctxActionID).(string); ok {
		return actionID
	}
	return ""
}
