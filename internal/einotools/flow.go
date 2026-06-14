package einotools

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"

	"github.com/V3teran/liusha/internal/flow"
)

// FlowReader 是 replay_flow 依赖的最小读接口（*flow.Store 自动满足）。
type FlowReader interface {
	GetByID(ctx context.Context, id int64) (flow.Flow, error)
}

const replayRespLimit = 256 * 1024 // 截断响应给 LLM 的上限（256 KiB）
const replayTimeout = 30 * time.Second

// replayMods 是 replay_flow 的可选修改；未指定字段全继承原请求。
// 全字段可选，带 ,omitempty 避免被误标 required（见 findings.go 详注）。
type replayMods struct {
	URL     string             `json:"url,omitempty"     jsonschema:"description=换完整 URL（含 path + query）"`
	Method  string             `json:"method,omitempty"  jsonschema:"description=换 HTTP method"`
	Headers map[string]*string `json:"headers,omitempty" jsonschema:"description=header dict 增删改；value=null 删该 header"`
	Body    *string            `json:"body,omitempty"    jsonschema:"description=替换整个 body"`
}

// replayFlowArgs 是 replay_flow 入参。
type replayFlowArgs struct {
	ID            int64      `json:"id"                      jsonschema:"required,description=原 flow id（来自 list_flows / view_flow）"`
	Modifications replayMods `json:"modifications,omitempty" jsonschema:"description=可选修改；未指定字段全继承原请求"`
}

// BuildReplayFlow 造原生 eino replay_flow 工具。owner 闭包捕获（防跨 owner replay）。
//
// v34+：直连目标（撤回 internal proxy）。重发流量不入字典——核心价值在 modifications
// 改一两个字段 + 其余全继承（cookie/CSRF/auth/form 字段），比手写 curl 准 100x。
func BuildReplayFlow(store FlowReader, ownerType, ownerID, hunterID string) (tool.BaseTool, error) {
	return utils.InferTool(
		"replay_flow",
		"重发历史 HTTP 流量；modifications 改部分字段，未指定的全继承原请求（cookie/CSRF/auth/form 字段）。"+
			"比手写 curl 准 100 倍——session 上下文自动保留。返回新响应详情。",
		func(ctx context.Context, in replayFlowArgs) (map[string]any, error) {
			if ownerID == "" {
				return nil, fmt.Errorf("replay_flow: owner 注入缺失")
			}
			if in.ID <= 0 {
				return nil, fmt.Errorf("id 必填且 > 0")
			}

			f, err := store.GetByID(ctx, in.ID)
			if err != nil {
				return nil, fmt.Errorf("flow %d 不存在或读失败: %w", in.ID, err)
			}
			if f.OwnerID != ownerID {
				return nil, fmt.Errorf("flow %d 不属于当前 owner（拒绝跨 owner replay）", in.ID)
			}

			// method/url/body 从原 flow 起步，应用 modifications
			method := f.Method
			if in.Modifications.Method != "" {
				method = strings.ToUpper(in.Modifications.Method)
			}
			rawURL := f.URL
			if in.Modifications.URL != "" {
				rawURL = in.Modifications.URL
			}
			body := f.RequestBody
			if in.Modifications.Body != nil {
				body = []byte(*in.Modifications.Body)
			}

			// 反序列化原 request_headers jsonb → map，apply mod 增删改（value=null 删）
			// 解析失败直接报错：headers 多含认证凭据，空 headers 重放会丢认证 → 误导性结果。
			headers := map[string]string{}
			if len(f.RequestHeaders) > 0 {
				if err := json.Unmarshal(f.RequestHeaders, &headers); err != nil {
					return nil, fmt.Errorf("解析 flow %d 的 request_headers 失败（数据损坏，拒绝空头重放）: %w", in.ID, err)
				}
			}
			for k, v := range in.Modifications.Headers {
				key := strings.ToLower(k)
				if v == nil {
					delete(headers, key)
				} else {
					headers[key] = *v
				}
			}

			req, err := http.NewRequestWithContext(ctx, method, rawURL, bytes.NewReader(body))
			if err != nil {
				return nil, fmt.Errorf("构造请求失败: %w", err)
			}
			for k, v := range headers {
				req.Header.Set(k, v) // canonical key，消解 content-type vs Content-Type 分歧
			}

			// v34+：直连目标。TLS InsecureSkipVerify 给 self-signed cert 目标用（pentest 常见）。
			client := &http.Client{
				Transport: &http.Transport{
					TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // pentest target may use self-signed cert
				},
				Timeout: replayTimeout,
			}
			resp, err := client.Do(req)
			if err != nil {
				return nil, fmt.Errorf("replay 请求失败: %w", err)
			}
			defer resp.Body.Close()

			respBody, _ := io.ReadAll(io.LimitReader(resp.Body, replayRespLimit))
			respHeaders := map[string]string{}
			for k, v := range resp.Header {
				if len(v) > 0 {
					respHeaders[strings.ToLower(k)] = strings.Join(v, ",")
				}
			}

			return map[string]any{
				"original_flow_id": f.ID,
				"replayed": map[string]any{
					"method":           method,
					"url":              rawURL,
					"request_headers":  headers,
					"request_body_len": len(body),
				},
				"response": map[string]any{
					"status_code":       resp.StatusCode,
					"response_headers":  respHeaders,
					"response_body":     string(respBody),
					"response_body_len": len(respBody),
				},
				"note": "v34+ 直连重发，新流量**不入字典**；仅返响应给本 hunter。要让流量入字典请用 browser_use（chromium CDP capture）",
			}, nil
		})
}
