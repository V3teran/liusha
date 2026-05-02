package probe

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/V3teran/liusha/internal/toolfx"
	"github.com/V3teran/liusha/internal/replay"
)

// bodyHintMaxBytes 是 LLM 看到的 body 摘要最大字节数：
// 完整 body 留在 Session.LastResponses 里供 heuristic / similarity 使用，
// 喂回 LLM 的 tool message 必须截断（黑客松借鉴 D：result_compress）。
const bodyHintMaxBytes = 400

// defaultConcurrency 与 replay.Engine 内部默认值保持一致，避免 schema/实际行为漂移。
const defaultConcurrency = 5

// ReplayMultiIdentity — BAC ReAct 第二步：用 Session.Identities 全身份并发重放一条 flow。
//
// 前置：必须先调 fetch_credentials 写满 Session.Identities，否则报错。
// 副作用：写 Session.LastFlow + Session.LastResponses，供 heuristic_check / compute_similarity 直接读取。
type ReplayMultiIdentity struct {
	Engine  *replay.Engine
	Flows   FlowReader
	Session *Session
}

// Name 返回动作名 "replay_multi_identity"。
func (a *ReplayMultiIdentity) Name() string { return "replay_multi_identity" }

// Description 给 LLM 看的简介。
func (a *ReplayMultiIdentity) Description() string {
	return "用 Session 内全部身份并发重放一条 flow（必须先调 fetch_credentials），返回各身份的 body_hint（≤400 byte）。"
}

// ParametersJSON 给出 flow_id / host 必填 + concurrency 默认 5 的 schema。
func (a *ReplayMultiIdentity) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{
  "type":"object",
  "properties": {
    "flow_id":{"type":"integer","description":"http_flow.id（int64）"},
    "host":{"type":"string","description":"目标 host，仅用于结果展示"},
    "concurrency":{"type":"integer","default":5,"minimum":1,"description":"并发上限"}
  },
  "required":["flow_id","host"]
}`)
}

// respSummary 是返回给 LLM 的瘦响应摘要：
//   - 不含完整 body，只有 ≤400 byte 的 hint。
//   - 完整 body / Headers 留在 Session.LastResponses 里给 heuristic / similarity。
type respSummary struct {
	Identity   string `json:"identity"`
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

// Execute 解析 args → 取 flow → 校验身份 → 并发重放 → 写 Session → 返回瘦摘要。
func (a *ReplayMultiIdentity) Execute(ctx context.Context, args json.RawMessage) (toolfx.Result, error) {
	var in struct {
		FlowID      int64  `json:"flow_id"`
		Host        string `json:"host"`
		Concurrency int    `json:"concurrency"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return toolfx.Result{}, fmt.Errorf("解析 replay_multi_identity 参数失败: %w", err)
	}
	if in.FlowID <= 0 {
		return toolfx.Result{}, fmt.Errorf("flow_id 必填且 > 0")
	}
	if len(a.Session.Identities) == 0 {
		return toolfx.Result{}, fmt.Errorf("session.Identities 为空，请先调 fetch_credentials")
	}
	if in.Concurrency <= 0 {
		in.Concurrency = defaultConcurrency
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
	resps, err := a.Engine.ReplayMultiIdentity(ctx, raw, a.Session.Identities, in.Concurrency)
	if err != nil {
		return toolfx.Result{}, fmt.Errorf("并发重放 flow %d 失败: %w", in.FlowID, err)
	}

	// 全 body 入 Session（供后续 heuristic / similarity）；瘦摘要喂 LLM。
	a.Session.LastResponses = resps
	a.Session.LastFlow = f

	out := replayOutput{
		FlowID:    f.ID,
		Method:    f.Method,
		URL:       f.URL,
		Count:     len(resps),
		Responses: make([]respSummary, 0, len(resps)),
	}
	for _, r := range resps {
		out.Responses = append(out.Responses, respSummary{
			Identity:   r.IdentityName,
			StatusCode: r.StatusCode,
			BodyHint:   truncateBytes(r.Body, bodyHintMaxBytes),
			Error:      r.ErrorMessage,
		})
	}
	enc, err := json.Marshal(out)
	if err != nil {
		return toolfx.Result{}, fmt.Errorf("序列化 replay_multi_identity 输出失败: %w", err)
	}
	return toolfx.Result{
		Output:  enc,
		Summary: fmt.Sprintf("replay_multi_identity flow=%d count=%d", f.ID, len(resps)),
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
