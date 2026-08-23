// Package httpreplay 是纯 HTTP 重放机制：在一条历史请求基础上做字段级改写、直连目标重发、
// 抓响应。零框架依赖（无外部编排框架、无 verifier）——工具层（replay_traffic）与验证层（Verifier
// 复现门）共用它，避免域/验证层为重放 HTTP 而拖进 agent 框架。
//
// 不判是否坐实漏洞：那是调用方（工具给 LLM 看 / Verifier 做坐实断言）的事。
package httpreplay

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
)

const (
	respLimit   = 256 * 1024 // 截断响应上限（256 KiB）
	dialTimeout = 30 * time.Second
)

// Source 是被重放的历史请求（工具层的 TrafficRecord / finding 锚定的源流量投影到此）。
type Source struct {
	ID      int64
	Method  string
	URL     string
	Headers json.RawMessage // 原 request headers（JSON object）
	Body    []byte
}

// Mods 是字段级改写：未指定的字段全继承原请求（cookie/CSRF/auth/form）。
// value=nil 表示删除该 header/query/body 字段。
type Mods struct {
	URL        string
	Method     string
	Headers    map[string]*string
	Query      map[string]*string
	Body       *string
	BodyFields map[string]*string
}

// Result 是一次重放的结构化结果。
type Result struct {
	Method          string
	URL             string
	RequestHeaders  map[string]string
	RequestBody     []byte
	StatusCode      int
	ResponseHeaders map[string]string
	ResponseBody    []byte
}

// Replay 按 mods 改写 src 并直连重发。TLS 跳过校验（pentest 目标常自签名）。
func Replay(ctx context.Context, src Source, mods Mods) (Result, error) {
	method := src.Method
	if mods.Method != "" {
		method = strings.ToUpper(mods.Method)
	}

	// headers 先解析（body 字段级改要据 content-type 选 JSON/form）。key 统一小写。
	// 解析失败直接报错：headers 多含认证凭据，空头重放丢认证 → 误导性结果。
	rawHeaders := map[string]string{}
	if len(src.Headers) > 0 {
		if err := json.Unmarshal(src.Headers, &rawHeaders); err != nil {
			return Result{}, fmt.Errorf("解析 traffic %d 的 request headers 失败（数据损坏，拒绝空头重放）: %w", src.ID, err)
		}
	}
	headers := map[string]string{}
	for k, v := range rawHeaders {
		headers[strings.ToLower(k)] = v
	}
	for k, v := range mods.Headers {
		key := strings.ToLower(k)
		if v == nil {
			delete(headers, key)
		} else {
			headers[key] = *v
		}
	}

	body := src.Body
	if mods.Body != nil {
		body = []byte(*mods.Body)
	}
	if len(mods.BodyFields) > 0 {
		patched, err := applyBodyFieldMods(body, headers["content-type"], mods.BodyFields)
		if err != nil {
			return Result{}, err
		}
		body = patched
	}

	rawURL := src.URL
	if mods.URL != "" {
		rawURL = mods.URL
	}
	if len(mods.Query) > 0 {
		patched, err := applyQueryMods(rawURL, mods.Query)
		if err != nil {
			return Result{}, err
		}
		rawURL = patched
	}

	req, err := http.NewRequestWithContext(ctx, method, rawURL, bytes.NewReader(body))
	if err != nil {
		return Result{}, fmt.Errorf("构造请求失败: %w", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // pentest target may use self-signed cert
		},
		Timeout: dialTimeout,
	}
	resp, err := client.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("replay 请求失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, respLimit))
	respHeaders := map[string]string{}
	for k, v := range resp.Header {
		if len(v) > 0 {
			respHeaders[strings.ToLower(k)] = strings.Join(v, ",")
		}
	}

	return Result{
		Method: method, URL: rawURL, RequestHeaders: headers, RequestBody: body,
		StatusCode: resp.StatusCode, ResponseHeaders: respHeaders, ResponseBody: respBody,
	}, nil
}

// applyQueryMods 在 rawURL 的 query 上字段级增删改（value=nil 删，其余继承），返回新 URL。
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

// applyBodyFieldMods 按 content-type 选 JSON/form 解析，字段级增删改（value=nil 删），其余继承。
// JSON 体新值按 JSON 解析以保留数字/布尔/对象类型，失败则当字符串。不支持的 content-type 报错。
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
				obj[k] = json.RawMessage(*v)
			} else {
				q, _ := json.Marshal(*v)
				obj[k] = q
			}
		}
		return json.Marshal(obj)
	case strings.Contains(ct, "x-www-form-urlencoded"), ct == "":
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
