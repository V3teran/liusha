package probe

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/V3teran/liusha/internal/replay"
	"github.com/V3teran/liusha/internal/toolruntime"
)

// fallback 常量：caller 未通过 ReplayMatrix 字段注入时使用。
// 正常路径由 cmd/scanner 从 cfg.Probe 注入（probe.Factory 装配时透传）。
const (
	// fallbackBodyHintMaxBytes：8192 byte 给 SQLi error-based 留足空间——HTML 报错常在
	// 响应中后段，2KB 经常被 nav/css/header 吃光看不到 SQL 错误关键字。与 yaml
	// vuln.flow_body_prompt_limit 对齐（同级粒度：原始 body vs 重放 body）。
	fallbackBodyHintMaxBytes   = 8192
	fallbackDefaultConcurrency = 5
)

// ReplayMatrix — 漏洞探针通用工具：用 ProbeState.Identities × variants 笛卡尔积并发重放一条 flow。
//
// 前置：必须先调 fetch_credentials 写满 ProbeState.Identities，否则报错。
// 副作用：写 ProbeState.LastFlow + ProbeState.LastResponses，供 check_heuristics / compute_similarity 直接读取。
//
// BAC 用法：identities=[全身份, 4 个] × variants=[baseline] → 4 条响应（仅身份替换不变形）。
// SQLi 用法（Step 2 起）：identities=[admin] × variants=[baseline, err_quote, bool_true, ...] → N 条响应。
type ReplayMatrix struct {
	Engine *replay.Engine
	Flows  FlowReader
	State  *ProbeState

	// v1.3：可选注入；零值走 fallback 常量，由 cmd/scanner 从 cfg.Probe 装配。
	BodyHintMaxBytes   int
	DefaultConcurrency int
}

func (a *ReplayMatrix) effectiveBodyHintMaxBytes() int {
	if a.BodyHintMaxBytes > 0 {
		return a.BodyHintMaxBytes
	}
	return fallbackBodyHintMaxBytes
}

func (a *ReplayMatrix) effectiveDefaultConcurrency() int {
	if a.DefaultConcurrency > 0 {
		return a.DefaultConcurrency
	}
	return fallbackDefaultConcurrency
}

// Name 返回动作名 "run_replay"。
func (a *ReplayMatrix) Name() string { return "run_replay" }

// Description 给 LLM 看的简介，强调 identity × variant 笛卡尔积语义。
func (a *ReplayMatrix) Description() string {
	return "用 ProbeState 内身份 × 请求变体笛卡尔积并发重放一条 flow（必须先调 fetch_credentials）。" +
		"variants 缺省时退化为单一 baseline（仅身份替换不变形请求）。" +
		"返回各 cell 的 body_hint（≤400 byte）；完整 body 留在 state 里给 heuristic / similarity。"
}

// ParametersJSON 给出 flow_id / host 必填 + concurrency 默认 5 的 schema；
// variants 可选，缺省时使用单一 baseline variant。
func (a *ReplayMatrix) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{
  "type":"object",
  "properties": {
    "flow_id":{"type":"integer","description":"http_flow.id（int64）"},
    "host":{"type":"string","description":"目标 host，仅用于结果展示"},
    "concurrency":{"type":"integer","default":5,"minimum":1,"description":"并发上限"},
    "variants":{
      "type":"array",
      "description":"请求变体列表；缺省时单一 baseline（passthrough）。每项含 name + mutation。",
      "items":{
        "type":"object",
        "properties":{
          "name":{"type":"string"},
          "mutation":{
            "type":"object",
            "properties":{
              "type":{"type":"string","enum":["passthrough","param_inject"],"description":"passthrough=仅替换身份不变形；param_inject=向指定字段注入 payload"},
              "where":{"type":"string","enum":["query","body_json","body_form","path_param"],"description":"param_inject 时必填：注入位置"},
              "field":{"type":"string","description":"param_inject 时必填：字段名（取自 extract_injection_points 的 key）"},
              "value":{"type":"string","description":"param_inject 时必填：payload 字符串，如 \" AND 1=1--\""},
              "mode":{"type":"string","enum":["replace","append"],"default":"replace","description":"replace=用 value 替换原字段值；append=拼接到原值后（保 SQL 闭合）"}
            },
            "required":["type"]
          }
        },
        "required":["name","mutation"]
      }
    }
  },
  "required":["flow_id","host"]
}`)
}

// respSummary 是返回给 LLM 的瘦响应摘要：
//   - 不含完整 body，只有 ≤400 byte 的 hint。
//   - 完整 body / Headers 留在 ProbeState.LastResponses 里给 heuristic / similarity。
//   - identity × variant 二维标签同时返回，方便 LLM 定位 cell。
type respSummary struct {
	Identity   string `json:"identity"`
	Variant    string `json:"variant"`
	StatusCode int    `json:"status_code"`
	BodyHint   string `json:"body_hint,omitempty"`
	Error      string `json:"error,omitempty"`
}

// replayOutput 是 Result.Output 的统一结构。
type replayOutput struct {
	FlowID    int64         `json:"flow_id"`
	Method    string        `json:"method"`
	URL       string        `json:"url"`
	Count     int           `json:"count"`
	Responses []respSummary `json:"responses"`
}

// Execute 解析 args → 取 flow → 校验身份 → 笛卡尔积并发重放 → 写 ProbeState → 返回瘦摘要。
func (a *ReplayMatrix) Execute(ctx context.Context, args json.RawMessage) (toolfx.Result, error) {
	var in struct {
		FlowID      int64            `json:"flow_id"`
		Host        string           `json:"host"`
		Concurrency int              `json:"concurrency"`
		Variants    []replay.Variant `json:"variants"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return toolfx.Result{}, fmt.Errorf("解析 run_replay 参数失败: %w", err)
	}
	if in.FlowID <= 0 {
		return toolfx.Result{}, fmt.Errorf("flow_id 必填且 > 0")
	}
	if len(a.State.Identities) == 0 {
		return toolfx.Result{}, fmt.Errorf("state.Identities 为空，请先调 fetch_credentials")
	}
	if in.Concurrency <= 0 {
		in.Concurrency = a.effectiveDefaultConcurrency()
	}

	f, err := a.Flows.GetByID(ctx, in.FlowID)
	if err != nil {
		return toolfx.Result{}, fmt.Errorf("读取 flow %d 失败: %w", in.FlowID, err)
	}

	raw := replay.RawRequest{
		Method:  f.Method,
		URL:     f.URL,
		Headers: rebuildHeaders(f.RequestHeaders),
		Body:    f.RequestBody,
	}
	resps, err := a.Engine.ReplayMatrix(ctx, raw, a.State.Identities, in.Variants, in.Concurrency)
	if err != nil {
		return toolfx.Result{}, fmt.Errorf("矩阵重放 flow %d 失败: %w", in.FlowID, err)
	}

	// 全 body 入 ProbeState（供后续 heuristic / similarity）；瘦摘要喂 LLM。
	a.State.LastResponses = resps
	a.State.LastFlow = f

	out := replayOutput{
		FlowID:    f.ID,
		Method:    f.Method,
		URL:       f.URL,
		Count:     len(resps),
		Responses: make([]respSummary, 0, len(resps)),
	}
	hintN := a.effectiveBodyHintMaxBytes()
	for _, r := range resps {
		out.Responses = append(out.Responses, respSummary{
			Identity:   r.IdentityName,
			Variant:    r.VariantName,
			StatusCode: r.StatusCode,
			BodyHint:   truncateBytes(r.Body, hintN),
			Error:      r.ErrorMessage,
		})
	}
	enc, err := json.Marshal(out)
	if err != nil {
		return toolfx.Result{}, fmt.Errorf("序列化 run_replay 输出失败: %w", err)
	}
	return toolfx.Result{
		Output:  enc,
		Summary: fmt.Sprintf("run_replay flow=%d count=%d", f.ID, len(resps)),
	}, nil
}

// truncateBytes 把 b 截到 max byte 并以 string 形式返回。
// b 可能含非 UTF-8 字节，但 bodyHintMaxBytes 只是粗略防爆，调用方可接受。
func truncateBytes(b []byte, max int) string {
	if len(b) <= max {
		return string(b)
	}
	return string(b[:max])
}

// rebuildHeaders 把存库的 jsonb headers 还原成 http.Header。
//
// 兼容两种存储形态：
//   - flat: {"Cookie":"a", "Accept":"json"}（V1 proxy 当前格式）
//   - 多值: {"Cookie":"a, b"} —— 按 ", " 切回多值
//
// 解析失败时退回空 Header，避免阻断 replay（错误以 Response.ErrorMessage 暴露）。
func rebuildHeaders(raw json.RawMessage) http.Header {
	if len(raw) == 0 {
		return http.Header{}
	}
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		return http.Header{}
	}
	h := http.Header{}
	for k, v := range m {
		for _, s := range strings.Split(v, ", ") {
			h.Add(k, s)
		}
	}
	return h
}
