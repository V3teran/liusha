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
    "identity":       {"type": ["string", "null"], "description": "使用指定身份的凭证（identity 名称），null 表示匿名请求（不注入任何凭证），省略则注入所有可用凭证"},
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

// 会话身份的 task 域化命名：自动会话身份存为 "task:{taskID}:session"——
// 共享容器/共享凭证库下，跨 task 的会话互染（A 任务登录态注入 B 任务、
// 并发同 host 互相覆盖）由此切断。用户/LLM 手工录入的身份不带 task: 前缀，
// 属 host 级共享（预录入语义），所有 task 可见可注入。
const (
	sessionIdentityPrefix = "task:"   // 自动身份保留前缀
	SessionIdentityName   = "session" // 身份名（与前缀拼接成完整 field）
)

// sessionIdentityName 返回本 task 的自动会话身份名（taskID 空时退化为全局名，
// 供无任务上下文的调用方如 judge 复核侧使用）。
func sessionIdentityName(taskID string) string {
	if taskID == "" {
		return SessionIdentityName
	}
	return sessionIdentityPrefix + taskID + ":" + SessionIdentityName
}

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
	Identity       *string           `json:"identity"` // nil=所有凭证, "name"=仅该身份, ""=匿名(无凭证)
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

	// 处理 identity 参数
	if a.Identity != nil && *a.Identity == "" {
		// identity="" 表示匿名请求，不注入任何凭证
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

	ids = selectApplicableIdentities(ids, t.deps.TaskID, a.Identity)
	if len(ids) == 0 {
		return nil
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].Name < ids[j].Name })

	appliedKeys := map[string]bool{} // "pos|key" 已注入（跨身份去重）
	var applied []appliedCredential
	for _, id := range ids {
		for _, c := range id.Credentials {
			if ac, ok := t.applyOneCredential(a, id.Name, c, appliedKeys); ok {
				applied = append(applied, ac)
			}
		}
	}
	return applied
}

// selectApplicableIdentities 做两层过滤：task 域隔离（只保留本 task 的自动会话身份 +
// host 级共享身份，其他 task 的会话身份不可见——共享库下的隔离边界）与显式身份挑选。
func selectApplicableIdentities(ids []credential.Identity, taskID string, identity *string) []credential.Identity {
	mine := sessionIdentityName(taskID)
	filtered := ids[:0]
	for _, id := range ids {
		if strings.HasPrefix(id.Name, sessionIdentityPrefix) && id.Name != mine {
			continue
		}
		// 如果指定了 identity，只保留该身份
		if identity != nil && id.Name != *identity {
			continue
		}
		filtered = append(filtered, id)
	}
	return filtered
}

// applyOneCredential 按凭证类型（headers/query/body）注入一条凭证；
// 已注入过（跨身份去重）或类型不适用时返回 ok=false。
func (t *httpRequestTool) applyOneCredential(a *httpRequestArgs, identityName string, c credential.Credential, appliedKeys map[string]bool) (appliedCredential, bool) {
	switch c.Type {
	case credential.TypeHeaders:
		return t.applyHeaderCredential(a, identityName, c, appliedKeys)
	case credential.TypeQuery:
		return t.applyQueryCredential(a, identityName, c, appliedKeys)
	case credential.TypeBody:
		return t.applyBodyCredential(a, identityName, c, appliedKeys)
	default:
		return appliedCredential{}, false
	}
}

// hasExplicitHeader 报告请求是否已显式携带同名头（大小写不敏感）。
func hasExplicitHeader(headers map[string]string, key string) bool {
	for k := range headers {
		if strings.EqualFold(k, key) {
			return true
		}
	}
	return false
}

// contentTypeOf 取请求头的 Content-Type 值（大小写不敏感；无则空串）。
func contentTypeOf(headers map[string]string) string {
	for k, v := range headers {
		if strings.EqualFold(k, "Content-Type") {
			return v
		}
	}
	return ""
}

// applyHeaderCredential 注入 header 类凭证（显式同名头优先，不覆盖）。
func (t *httpRequestTool) applyHeaderCredential(a *httpRequestArgs, identityName string, c credential.Credential, appliedKeys map[string]bool) (appliedCredential, bool) {
	if hasExplicitHeader(a.Headers, c.Key) || appliedKeys["h|"+strings.ToLower(c.Key)] {
		return appliedCredential{}, false
	}
	if a.Headers == nil {
		a.Headers = map[string]string{}
	}
	a.Headers[c.Key] = c.Value
	appliedKeys["h|"+strings.ToLower(c.Key)] = true
	return appliedCredential{Identity: identityName, Position: "headers", Key: c.Key}, true
}

// applyQueryCredential 注入 query 类凭证（URL 已有同名参数则视为覆盖，不重复注入）。
func (t *httpRequestTool) applyQueryCredential(a *httpRequestArgs, identityName string, c credential.Credential, appliedKeys map[string]bool) (appliedCredential, bool) {
	if appliedKeys["q|"+c.Key] {
		return appliedCredential{}, false
	}
	u, pErr := url.Parse(a.URL)
	if pErr != nil {
		return appliedCredential{}, false
	}
	q := u.Query()
	if _, exists := q[c.Key]; exists {
		appliedKeys["q|"+c.Key] = true
		return appliedCredential{}, false
	}
	q.Set(c.Key, c.Value)
	u.RawQuery = q.Encode()
	a.URL = u.String()
	appliedKeys["q|"+c.Key] = true
	return appliedCredential{Identity: identityName, Position: "query", Key: c.Key}, true
}

// applyBodyCredential 注入 body 类凭证（仅 form-urlencoded 体可安全注入；
// 显式 JSON 等类型不动，防破坏结构）。
func (t *httpRequestTool) applyBodyCredential(a *httpRequestArgs, identityName string, c credential.Credential, appliedKeys map[string]bool) (appliedCredential, bool) {
	if appliedKeys["b|"+c.Key] {
		return appliedCredential{}, false
	}
	ct := contentTypeOf(a.Headers)
	if ct != "" && !strings.Contains(strings.ToLower(ct), "urlencoded") {
		return appliedCredential{}, false
	}
	vals, pErr := url.ParseQuery(a.Body)
	if pErr != nil {
		return appliedCredential{}, false // 非 form 形态，跳过
	}
	if _, exists := vals[c.Key]; exists {
		appliedKeys["b|"+c.Key] = true
		return appliedCredential{}, false
	}
	vals.Set(c.Key, c.Value)
	a.Body = vals.Encode()
	if ct == "" && a.Headers != nil {
		a.Headers["Content-Type"] = "application/x-www-form-urlencoded"
	} else if a.Headers == nil {
		a.Headers = map[string]string{"Content-Type": "application/x-www-form-urlencoded"}
	}
	appliedKeys["b|"+c.Key] = true
	return appliedCredential{Identity: identityName, Position: "body", Key: c.Key}, true
}

// syncSessionToStore 把当前会话 Cookie 头合并写入凭证库保留身份 "session"（③B：
// store 是事实源，jar 只是缓存）。防抖：值未变不写；全空则清 Cookie 项（保留该
// 身份下其他凭证）。ttl 恒为 0——BatchSave 的 ttl 是 host 级 EXPIRE，会误杀
// 同 host 用户预录入身份。
func (t *httpRequestTool) syncSessionToStore(ctx context.Context, host string) {
	if t.deps.Creds == nil || host == "" {
		return
	}
	mine := sessionIdentityName(t.deps.TaskID)
	ch := t.cookieHeader(host)

	t.mu.Lock()
	last, seen := t.lastWritten[host]
	t.mu.Unlock()
	if seen && last == ch {
		return // 防抖：值未变
	}

	// 合并语义：保留本 task 会话身份下非 Cookie 的其他凭证（凭证数量不定）
	var session credential.Identity
	if ids, err := t.deps.Creds.GetIdentitiesByHost(ctx, host); err == nil {
		for _, id := range ids {
			if id.Name == mine {
				session = id
				break
			}
		}
	}
	session.Name = mine
	if session.Role == "" {
		session.Role = SessionIdentityName
	}
	kept := session.Credentials[:0:0]
	for _, c := range session.Credentials {
		if c.Type != credential.TypeHeaders || c.Key != "Cookie" {
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
		"数量与位置不定）自动注入，显式传入的键优先；响应 Set-Cookie 自动入库（identity=session)。"
}
func (t *httpRequestTool) Schema() json.RawMessage { return httpRequestSchema }

func (t *httpRequestTool) Execute(ctx context.Context, args json.RawMessage) (registry.ToolResult, error) {
	// 1. 解析并验证参数
	var a httpRequestArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return registry.ToolResult{Error: "http_request: 解析参数失败: " + err.Error()}, nil
	}
	if errMsg := validateAndNormalizeArgs(&a); errMsg != nil {
		return registry.ToolResult{Error: *errMsg}, nil
	}

	// 2. 构建 HTTP 请求
	req, applied, err := t.buildHTTPRequest(ctx, &a)
	if err != nil {
		return registry.ToolResult{Error: "http_request: 构建请求失败: " + err.Error()}, nil
	}

	// 3. 执行 HTTP 请求
	resp, durMs, err := executeHTTPRequest(ctx, req, a.TimeoutSeconds)
	if err != nil {
		return registry.ToolResult{Error: "http_request: " + err.Error()}, nil
	}
	defer resp.Body.Close()

	// 4. 读取响应体
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return registry.ToolResult{Error: "http_request: 读取响应失败: " + err.Error()}, nil
	}

	// 5. 处理响应 Cookie
	t.handleResponseCookies(ctx, req, resp)

	// 6. 记录流量到弹药库
	trafficID := t.recordTraffic(ctx, req, resp, &a, respBody, durMs)

	// 7. 构建并返回结果
	return t.buildToolResult(req, resp, &a, respBody, durMs, trafficID, applied), nil
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
