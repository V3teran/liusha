package actions

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/V3teran/liusha/internal/agent/action"
	"github.com/V3teran/liusha/internal/flow"
	"github.com/V3teran/liusha/internal/window"
)

// WindowStore 是 ReadWindow 依赖的最小接口：读单窗 + 标记消费。
// 由 *window.Store 自动满足（GetByID + MarkConsumed）。
type WindowStore interface {
	GetByID(ctx context.Context, id string) (window.Window, error)
	MarkConsumed(ctx context.Context, id string) error
}

// FlowReader 是 ReadWindow 依赖的最小接口：按 id 反查 flow。
// 由 *flow.Store 自动满足。
type FlowReader interface {
	GetByID(ctx context.Context, id int64) (flow.Flow, error)
}

// ReadWindow — Sniffer ReAct 专属 action：
// 按 window_id 拉取 traffic_window 的 N 个 flow ref，反查每条 flow 的
// method/url/status_code 摘要（不带 body 节省 token），并自动把窗口
// 推进到 consumed。
//
// Result.Output：{window_id, flows: [{id, method, url, status_code}], consumed: bool}
// Result.Summary：≤200 字摘要，给 Observer 滑动窗压缩历史用（黑客松借鉴）。
type ReadWindow struct {
	Windows WindowStore
	Flows   FlowReader
}

// Name 返回动作名 "read_window"。
func (a *ReadWindow) Name() string { return "read_window" }

// Description 给 LLM 看的简介。
func (a *ReadWindow) Description() string {
	return "读取一个 traffic_window 内全部 flow 的 method/url/status_code 摘要，并自动标记窗口为 consumed。"
}

// ParametersJSON 给出 window_id 必填 schema。
func (a *ReadWindow) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{
  "type":"object",
  "properties": {
    "window_id":{"type":"string","description":"traffic_window UUID"}
  },
  "required":["window_id"]
}`)
}

// flowSummary 是返回给 LLM 的 flow 瘦摘要，刻意不含 body / headers，控制 token。
type flowSummary struct {
	ID         int64  `json:"id"`
	Method     string `json:"method"`
	URL        string `json:"url"`
	StatusCode int    `json:"status_code"`
}

// readWindowOutput 是 Result.Output 的统一结构。
type readWindowOutput struct {
	WindowID string        `json:"window_id"`
	Flows    []flowSummary `json:"flows"`
	Consumed bool          `json:"consumed"`
}

// Execute 解析 args → 拉窗口 → 反查每条 flow → MarkConsumed → 返回摘要。
func (a *ReadWindow) Execute(ctx context.Context, args json.RawMessage) (action.Result, error) {
	var in struct {
		WindowID string `json:"window_id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return action.Result{}, fmt.Errorf("解析 read_window 参数失败: %w", err)
	}
	if in.WindowID == "" {
		return action.Result{}, fmt.Errorf("window_id 必填")
	}

	w, err := a.Windows.GetByID(ctx, in.WindowID)
	if err != nil {
		return action.Result{}, fmt.Errorf("读取窗口 %s 失败: %w", in.WindowID, err)
	}

	out := readWindowOutput{WindowID: in.WindowID, Flows: make([]flowSummary, 0, len(w.Flows))}
	for _, ref := range w.Flows {
		f, err := a.Flows.GetByID(ctx, ref.ID)
		if err != nil {
			// 单条 flow 缺失（被截断 / 删除）不应阻断整窗：跳过即可。
			continue
		}
		out.Flows = append(out.Flows, flowSummary{
			ID:         f.ID,
			Method:     f.Method,
			URL:        f.URL,
			StatusCode: f.StatusCode,
		})
	}

	// 即使 LLM 不读 result，也已推进 consumed —— 防重复消费。
	if err := a.Windows.MarkConsumed(ctx, in.WindowID); err != nil {
		return action.Result{}, fmt.Errorf("标记窗口 %s consumed 失败: %w", in.WindowID, err)
	}
	out.Consumed = true

	enc, err := json.Marshal(out)
	if err != nil {
		return action.Result{}, fmt.Errorf("序列化 read_window 输出失败: %w", err)
	}
	return action.Result{
		Output:  enc,
		Summary: buildSummary(in.WindowID, out.Flows),
	}, nil
}

// buildSummary 拼一行 ≤200 字的中文摘要给 Observer 用。
//
// 形如："window=w1 flows=3 [GET 200 /a; POST 401 /b; DELETE 500 /c]"
// 超过 200 个 rune 时尾部截断 + "..."。
func buildSummary(windowID string, flows []flowSummary) string {
	const maxRunes = 200
	head := fmt.Sprintf("window=%s flows=%d", windowID, len(flows))
	if len(flows) == 0 {
		return head
	}
	body := " ["
	for i, f := range flows {
		if i > 0 {
			body += "; "
		}
		body += fmt.Sprintf("%s %d %s", f.Method, f.StatusCode, f.URL)
	}
	body += "]"
	full := head + body
	r := []rune(full)
	if len(r) <= maxRunes {
		return full
	}
	return string(r[:maxRunes-3]) + "..."
}
