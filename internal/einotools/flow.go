package einotools

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	URL        string             `json:"url,omitempty"         jsonschema:"description=换完整 URL（含 path + query）；只改个别 query 参数用 query 字段更省"`
	Method     string             `json:"method,omitempty"      jsonschema:"description=换 HTTP method"`
	Headers    map[string]*string `json:"headers,omitempty"     jsonschema:"description=字段级改 header：key=header 名 value=新值，value=null 删该 header；未列出的全继承（换 Cookie/Authorization 测越权、删之测未授权常用）"`
	Query      map[string]*string `json:"query,omitempty"       jsonschema:"description=字段级改 URL query 参数：key=参数名 value=新值，value=null 删；未列出的全继承（改 id 测 IDOR、删 token 测未授权常用）"`
	Body       *string            `json:"body,omitempty"        jsonschema:"description=整体替换 body（大改或非 form/JSON 体用它）"`
	BodyFields map[string]*string `json:"body_fields,omitempty" jsonschema:"description=字段级改 body（form-urlencoded / JSON 自动识别）：key=字段名 value=新值（JSON 体按 JSON 解析保留数字/布尔类型，否则当字符串），value=null 删；未列出的全继承（改单个 user_id 做 IDOR、换 body 里 CSRF 常用）"`
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
		"重发历史 HTTP 流量；modifications 可字段级改 header/query/body 单个字段或整体替换，未指定的全继承原请求（cookie/CSRF/auth/form 字段）。"+
			"换身份值测越权、删/换凭证测未授权、改业务字段做 IDOR/fuzz——比手写 curl 准 100 倍，session 上下文自动保留。返回新响应详情。",
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

			method := f.Method
			if in.Modifications.Method != "" {
				method = strings.ToUpper(in.Modifications.Method)
			}

			// headers 先解析（body 字段级修改要据 content-type 选 JSON / form）。key 统一小写便于查 content-type；
			// 发包时 req.Header.Set 会 canonical 化。解析失败直接报错：headers 多含认证凭据，空 headers 重放丢认证 → 误导性结果。
			rawHeaders := map[string]string{}
			if len(f.RequestHeaders) > 0 {
				if err := json.Unmarshal(f.RequestHeaders, &rawHeaders); err != nil {
					return nil, fmt.Errorf("解析 flow %d 的 request_headers 失败（数据损坏，拒绝空头重放）: %w", in.ID, err)
				}
			}
			headers := map[string]string{}
			for k, v := range rawHeaders {
				headers[strings.ToLower(k)] = v
			}
			for k, v := range in.Modifications.Headers { // 字段级增删改（value=null 删）
				key := strings.ToLower(k)
				if v == nil {
					delete(headers, key)
				} else {
					headers[key] = *v
				}
			}

			// body：先整体替换（若给）起步，再叠加字段级 BodyFields（form/JSON 自动识别，单字段增删改）。
			body := f.RequestBody
			if in.Modifications.Body != nil {
				body = []byte(*in.Modifications.Body)
			}
			if len(in.Modifications.BodyFields) > 0 {
				patched, perr := applyBodyFieldMods(body, headers["content-type"], in.Modifications.BodyFields)
				if perr != nil {
					return nil, perr
				}
				body = patched
			}

			// url：先整体替换（若给）起步，再叠加字段级 Query（单参数增删改）。
			rawURL := f.URL
			if in.Modifications.URL != "" {
				rawURL = in.Modifications.URL
			}
			if len(in.Modifications.Query) > 0 {
				patched, perr := applyQueryMods(rawURL, in.Modifications.Query)
				if perr != nil {
					return nil, perr
				}
				rawURL = patched
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

// applyQueryMods 在 rawURL 的 query 上做字段级增删改（value=null 删，其余继承），返回新 URL。
func applyQueryMods(rawURL string, mods map[string]*string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("query mods: 解析 URL 失败: %w", err)
	}
	q := u.Query()
	for k, v := range mods {
		if v == nil {
			q.Del(k)
		} else {
			q.Set(k, *v)
		}
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// applyBodyFieldMods 在 body 上做字段级增删改：按 content-type 选 JSON / form-urlencoded 解析，
// 改/删指定字段（value=null 删），其余继承。JSON 体的新值按 JSON 解析以保留数字/布尔/对象类型，
// 解析失败则当字符串。不支持的 content-type 报错（让 LLM 改用整体 body 替换）。
func applyBodyFieldMods(body []byte, contentType string, mods map[string]*string) ([]byte, error) {
	ct := strings.ToLower(contentType)
	switch {
	case strings.Contains(ct, "json"):
		obj := map[string]json.RawMessage{}
		if len(bytes.TrimSpace(body)) > 0 {
			if err := json.Unmarshal(body, &obj); err != nil {
				return nil, fmt.Errorf("body_fields: 原 body 不是 JSON 对象，无法字段级修改（改用整体 body 替换）: %w", err)
			}
		}
		for k, v := range mods {
			if v == nil {
				delete(obj, k)
				continue
			}
			if json.Valid([]byte(*v)) {
				obj[k] = json.RawMessage(*v) // 保留数字/布尔/对象类型
			} else {
				q, _ := json.Marshal(*v) // 当字符串
				obj[k] = q
			}
		}
		return json.Marshal(obj)
	case strings.Contains(ct, "x-www-form-urlencoded"), ct == "":
		// content-type 缺省按 form 兜底（pentest 表单站常见）；解析失败报错让 LLM 改整体替换。
		vals, err := url.ParseQuery(string(body))
		if err != nil {
			return nil, fmt.Errorf("body_fields: 原 body 不是 form-urlencoded（改用整体 body 替换）: %w", err)
		}
		for k, v := range mods {
			if v == nil {
				vals.Del(k)
			} else {
				vals.Set(k, *v)
			}
		}
		return []byte(vals.Encode()), nil
	default:
		return nil, fmt.Errorf("body_fields: 不支持的 content-type %q（仅 JSON / form-urlencoded；其它请用整体 body 替换）", contentType)
	}
}
