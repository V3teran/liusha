package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/V3teran/liusha/internal/credential"

	"github.com/V3teran/liusha/internal/registry"
	"github.com/V3teran/liusha/internal/traffic"
)

// ─── http_request ────────────────────────────────────────────────────────────
//
// 带抓流的 typed HTTP 工具：主进程侧发请求 + 落 agent_traffic（返回 traffic_id）。
//
// 架构位置：它是 browser-svc.py（CDP capture）的对位物——容器内 mitm 抓流层缺失时
// （Dockerfile 未装 mitmproxy），CLI 工具的 HTTP 无法进入复现弹药库。本工具把
// executor 的 HTTP 操作收拢到 typed 通道：请求自动入 agent_traffic → LLM 拿到
// traffic_id → write_observation(repro={traffic_id, assert}) → evaluator 重放坐实。
// 晋升链的完整闭环依赖此工具提供的「可复现原语」。

var httpRequestSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "url":            {"type": "string", "description": "完整 URL（含 http/https）"},
    "method":         {"type": "string", "enum": ["GET","POST","PUT","DELETE","PATCH","HEAD","OPTIONS"], "description": "HTTP 方法，默认 GET"},
    "headers":        {"type": "object", "description": "请求头 key→value（可选）"},
    "body":           {"type": "string", "description": "请求体（可选）"},
    "timeout_seconds": {"type": "integer", "description": "超时秒数，默认 30，上限 120"}
  },
  "required": ["url"]
}`)

const (
	httpReqMaxTimeout   = 120
	httpRespBodySnippet = 4096 // 回给 LLM 的响应体截断
)

// httpRequestTool 自带 per-task cookie jar 与凭证库（credential store）双向集成：
//
//	请求侧（②）：按 host 读取库中全部身份的全部凭证（数量与位置均不定——
//	N 凭证 × headers/query/body 三位置），逐条按位置注入；显式传入的键优先，
//	同键冲突时先到（身份名序）先得。Cookie 只是 headers 位置的一种。
//
//	响应侧（③B）：jar 仅作进程内缓存；Set-Cookie 变更时把完整 Cookie 头
//	以保留身份 "session" 合并写入凭证库（store 是唯一事实源）。防抖：值未
//	变化不写；ttl 必须 0——BatchSave 的 ttl 是 host 级 EXPIRE，带 ttl 会
//	误杀同 host 下用户预录入身份的存活期。
type httpRequestTool struct {
	registry.BaseTool
	deps Deps

	mu          sync.Mutex
	jar         map[string]string // "host|name" → value（缓存）
	lastWritten map[string]string // host → 上次写入 store 的 Cookie 头（防抖）
}

// SessionIdentityName 是自动会话身份的保留名——其 Cookie 凭证由本工具自动
// 管理（合并保留该身份下其他凭证）；用户/LLM 手工录入请用其他名字。
const SessionIdentityName = "session"

func newHTTPRequestTool(deps Deps, timeout time.Duration, safe bool) *httpRequestTool {
	t := &httpRequestTool{deps: deps, jar: map[string]string{}, lastWritten: map[string]string{}}
	t.SetTimeout(timeout)
	t.SetConcurrencySafe(safe)
	return t
}

// cookieHeader 组装指定 host 的会话 cookie（name=value; ...），无则空串。
func (t *httpRequestTool) cookieHeader(host string) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	var pairs []string
	for k, v := range t.jar {
		if strings.HasPrefix(k, host+"|") {
			pairs = append(pairs, strings.TrimPrefix(k, host+"|")+"="+v)
		}
	}
	sort.Strings(pairs)
	return strings.Join(pairs, "; ")
}

// storeCookies 落存响应 Set-Cookie（含 Max-Age<1 的删除语义：值清空即移除）。
func (t *httpRequestTool) storeCookies(host string, cookies []*http.Cookie) {
	if len(cookies) == 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, c := range cookies {
		key := host + "|" + c.Name
		if c.MaxAge < 0 {
			delete(t.jar, key)
			continue
		}
		t.jar[key] = c.Value
	}
}

// httpRequestArgs 是 http_request 的请求参数（凭证注入需在构造 http.Request
// 前变换这些原始值，故提为具名类型）。
type httpRequestArgs struct {
	URL            string            `json:"url"`
	Method         string            `json:"method"`
	Headers        map[string]string `json:"headers"`
	Body           string            `json:"body"`
	TimeoutSeconds int               `json:"timeout_seconds"`
}

// appliedCredential 是注入明细（值不回显——减少凭证在 LLM 上下文的暴露面）。
type appliedCredential struct {
	Identity string `json:"identity"`
	Position string `json:"position"` // headers / query / body
	Key      string `json:"key"`
}

// applyStoredCredentials 把凭证库中该 host 的全部身份凭证按位置注入请求参数。
// 凭证数量与位置均不定（N 凭证 × headers/query/body）；冲突规则：显式传入的键
// 优先，库内同键先到（身份名序）先得。best-effort：库不可用时跳过（jar 兜底）。
func (t *httpRequestTool) applyStoredCredentials(ctx context.Context, a *httpRequestArgs) []appliedCredential {
	if t.deps.Creds == nil {
		return nil
	}
	host := hostOf(a.URL)
	if host == "" {
		return nil
	}
	ids, err := t.deps.Creds.GetIdentitiesByHost(ctx, host)
	if err != nil || len(ids) == 0 {
		return nil // 库异常/空：不阻塞请求，会话头走 jar 兜底
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].Name < ids[j].Name })

	var applied []appliedCredential
	hasExplicit := func(key string) bool {
		for k := range a.Headers {
			if strings.EqualFold(k, key) {
				return true
			}
		}
		return false
	}
	appliedKeys := map[string]bool{} // "pos|key" 已注入（跨身份去重）

	for _, id := range ids {
		for _, c := range id.Credentials {
			switch c.Type {
			case credential.TypeHeaders:
				if hasExplicit(c.Key) || appliedKeys["h|"+strings.ToLower(c.Key)] {
					continue
				}
				if a.Headers == nil {
					a.Headers = map[string]string{}
				}
				a.Headers[c.Key] = c.Value
				appliedKeys["h|"+strings.ToLower(c.Key)] = true
				applied = append(applied, appliedCredential{Identity: id.Name, Position: "headers", Key: c.Key})
			case credential.TypeQuery:
				if appliedKeys["q|"+c.Key] {
					continue
				}
				u, pErr := url.Parse(a.URL)
				if pErr != nil {
					continue
				}
				q := u.Query()
				if _, exists := q[c.Key]; exists {
					appliedKeys["q|"+c.Key] = true // URL 里已有（含显式）视为覆盖
					continue
				}
				q.Set(c.Key, c.Value)
				u.RawQuery = q.Encode()
				a.URL = u.String()
				appliedKeys["q|"+c.Key] = true
				applied = append(applied, appliedCredential{Identity: id.Name, Position: "query", Key: c.Key})
			case credential.TypeBody:
				if appliedKeys["b|"+c.Key] {
					continue
				}
				// 仅 form-urlencoded 体可安全注入；显式 JSON 等类型不动（防破坏结构）。
				ct := ""
				for k, v := range a.Headers {
					if strings.EqualFold(k, "Content-Type") {
						ct = v
					}
				}
				if ct != "" && !strings.Contains(strings.ToLower(ct), "urlencoded") {
					continue
				}
				vals, pErr := url.ParseQuery(a.Body)
				if pErr != nil {
					continue // 非 form 形态，跳过
				}
				if _, exists := vals[c.Key]; exists {
					appliedKeys["b|"+c.Key] = true
					continue
				}
				vals.Set(c.Key, c.Value)
				a.Body = vals.Encode()
				if ct == "" && a.Headers != nil {
					a.Headers["Content-Type"] = "application/x-www-form-urlencoded"
				} else if a.Headers == nil {
					a.Headers = map[string]string{"Content-Type": "application/x-www-form-urlencoded"}
				}
				appliedKeys["b|"+c.Key] = true
				applied = append(applied, appliedCredential{Identity: id.Name, Position: "body", Key: c.Key})
			}
		}
	}
	return applied
}

// syncSessionToStore 把当前会话 Cookie 头合并写入凭证库保留身份 "session"（③B：
// store 是事实源，jar 只是缓存）。防抖：值未变不写；全空则清 Cookie 项（保留该
// 身份下其他凭证）。ttl 恒为 0——BatchSave 的 ttl 是 host 级 EXPIRE，会误杀
// 同 host 用户预录入身份。
func (t *httpRequestTool) syncSessionToStore(ctx context.Context, host string) {
	if t.deps.Creds == nil || host == "" {
		return
	}
	ch := t.cookieHeader(host)

	t.mu.Lock()
	last, seen := t.lastWritten[host]
	t.mu.Unlock()
	if seen && last == ch {
		return // 防抖：值未变
	}

	// 合并语义：保留 session 身份下非 Cookie 的其他凭证（凭证数量不定）
	var session credential.Identity
	if ids, err := t.deps.Creds.GetIdentitiesByHost(ctx, host); err == nil {
		for _, id := range ids {
			if id.Name == SessionIdentityName {
				session = id
				break
			}
		}
	}
	session.Name = SessionIdentityName
	if session.Role == "" {
		session.Role = SessionIdentityName
	}
	kept := session.Credentials[:0:0]
	for _, c := range session.Credentials {
		if !(c.Type == credential.TypeHeaders && c.Key == "Cookie") {
			kept = append(kept, c)
		}
	}
	if ch != "" {
		kept = append(kept, credential.Credential{Type: credential.TypeHeaders, Key: "Cookie", Value: ch})
	}
	session.Credentials = kept

	if err := t.deps.Creds.BatchSave(ctx, map[string][]credential.Identity{
		host: {session},
	}, 0); err != nil {
		return // best-effort：写失败不阻塞响应返回，jar 缓存仍可用
	}
	t.mu.Lock()
	t.lastWritten[host] = ch
	t.mu.Unlock()
}

// hostOf 从 URL 提取 host:port（与 jar 的隔离粒度一致）。
func hostOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Host
}

// sessionCookieNames 返回 host 当前会话 cookie 名列表（输出可见性）。
func (t *httpRequestTool) sessionCookieNames(host string) []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	var names []string
	for k := range t.jar {
		if strings.HasPrefix(k, host+"|") {
			names = append(names, strings.TrimPrefix(k, host+"|"))
		}
	}
	sort.Strings(names)
	return names
}

func (t *httpRequestTool) Name() string { return "http_request" }
func (t *httpRequestTool) ShortDesc() string {
	return "发 HTTP 请求并返回完整 request/response"
}
func (t *httpRequestTool) Desc() string {
	return "发送 HTTP 请求并返回完整的 request（method/url/headers/body）与 response——" +
		"write_observation 据此构造自包含复现配方（HTTP 发现的漏洞从本工具返回的 request 拷贝改造；" +
		"浏览器/非 HTTP 发现走 generic steps）。凭证库中该 host 的已录入凭证（headers/query/body 位置，" +
		"数量与位置不定）自动注入，显式传入的键优先；响应 Set-Cookie 自动入库（identity=session）。" +
		"带 CSRF 防护的表单登录两步走：先 GET 表单页，从 HTML hidden input 提取 token 字段（记下其 name），" +
		"再连同凭证一起 POST——只发凭证不带 token 会被拒绝。"
}
func (t *httpRequestTool) Schema() json.RawMessage { return httpRequestSchema }

func (t *httpRequestTool) Execute(ctx context.Context, args json.RawMessage) (registry.ToolResult, error) {
	var a httpRequestArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return registry.ToolResult{Error: "http_request: 解析参数失败: " + err.Error()}, nil
	}
	if a.URL == "" {
		return registry.ToolResult{Error: "http_request: url 必填"}, nil
	}
	if a.Method == "" {
		a.Method = http.MethodGet
	}
	if a.TimeoutSeconds <= 0 {
		a.TimeoutSeconds = 30
	}
	if a.TimeoutSeconds > httpReqMaxTimeout {
		a.TimeoutSeconds = httpReqMaxTimeout
	}

	// 凭证注入（②）：凭证库中该 host 的全部身份 × 全部凭证（数量、位置均不定）
	// 按位置注入。优先级：显式传入的键 > 库（身份名序先到先得）> jar 缓存兜底。
	applied := t.applyStoredCredentials(ctx, &a)

	req, err := http.NewRequestWithContext(ctx, a.Method, a.URL, strings.NewReader(a.Body))
	if err != nil {
		return registry.ToolResult{Error: "http_request: 构造请求失败: " + err.Error()}, nil
	}
	for k, v := range a.Headers {
		req.Header.Set(k, v)
	}
	// jar 兜底：库读取失败或库中尚无 Cookie 项时，用进程内缓存补会话头。
	if req.Header.Get("Cookie") == "" {
		if ch := t.cookieHeader(req.URL.Host); ch != "" {
			req.Header.Set("Cookie", ch)
		}
	}
	if a.Body != "" && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	client := &http.Client{
		Timeout: time.Duration(a.TimeoutSeconds) * time.Second,
		// 不自动跳转：跳转会隐藏 30x 语义（认证绕过判定依赖原始响应码）。
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	start := time.Now()
	resp, err := client.Do(req)
	durMs := int(time.Since(start).Milliseconds())
	if err != nil {
		return registry.ToolResult{Error: "http_request: 请求失败: " + err.Error()}, nil
	}
	defer func() { _ = resp.Body.Close() }()

	// 响应 Set-Cookie 入 jar（缓存）+ 变更时合并写入凭证库（事实源，③B）。
	t.storeCookies(req.URL.Host, resp.Cookies())
	t.syncSessionToStore(ctx, req.URL.Host)

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 上限 1MiB

	// 抓流入弹药库（best-effort：落库失败不阻塞请求本身，但 LLM 拿不到 traffic_id）
	trafficID := int64(0)
	if t.deps.AgentStore != nil {
		reqHeaders, _ := json.Marshal(flattenHeaders(req.Header))
		respHeaders, _ := json.Marshal(flattenHeaders(resp.Header))
		id, appendErr := t.deps.AgentStore.Append(ctx, traffic.AgentTraffic{
			TaskID:          t.deps.TaskID,
			AgentID:         t.deps.AgentID,
			Tool:            "http_request",
			Method:          a.Method,
			URL:             a.URL,
			RequestHeaders:  reqHeaders,
			RequestBody:     []byte(a.Body),
			StatusCode:      resp.StatusCode,
			ResponseHeaders: respHeaders,
			ResponseBody:    respBody,
			DurationMs:      durMs,
		})
		if appendErr != nil {
			// 记录失败只影响复现能力，不影响本次观测
			_ = appendErr
		}
		trafficID = id
	}

	snippet := respBody
	if len(snippet) > httpRespBodySnippet {
		snippet = snippet[:httpRespBodySnippet]
	}

	// 返回完整的 request/response 信息，供 write_observation 构造自包含的 repro。
	// request.headers 回显实际发送的最终 headers（含自动补的 Content-Type 与会话
	// Cookie）——LLM 拷贝此对象构造 repro.request 才能忠实重放。
	out, _ := json.Marshal(map[string]interface{}{
		"traffic_id": trafficID,
		"session": map[string]interface{}{
			"cookies":             t.sessionCookieNames(req.URL.Host),
			"applied_credentials": applied, // [{identity,position,key}]——值不回显
		},
		"request": map[string]interface{}{
			"method":  a.Method,
			"url":     a.URL,
			"headers": flattenHeaders(req.Header),
			"body":    a.Body,
		},
		"response": map[string]interface{}{
			"status_code":  resp.StatusCode,
			"headers":      flattenHeaders(resp.Header),
			"body":         string(snippet),
			"truncated":    len(respBody) > len(snippet),
			"duration_ms":  durMs,
			"content_type": resp.Header.Get("Content-Type"),
		},
	})
	return registry.ToolResult{Output: string(out)}, nil
}

// flattenHeaders 把 http.Header 摊平成单值 map（多值取第一个）——agent_traffic 的
// request/response_headers 列按单值 JSON 对象存（与 browser/CLI 抓流口径一致）。
func flattenHeaders(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k, vs := range h {
		if len(vs) > 0 {
			out[strings.ToLower(k)] = vs[0]
		}
	}
	return out
}
