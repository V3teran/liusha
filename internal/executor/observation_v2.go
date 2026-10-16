package executor

import (
	"encoding/json"
	"time"

	"github.com/V3teran/liusha/internal/proxy"
)

// Observation 是 Executor 的观察结果
//
// 设计原则（治本方案）：
//   - 不依赖 Executor 生成的断言（断言容易错）
//   - 传递完整的执行轨迹（Evaluator 看到 Executor 看到的一切）
//   - Evaluator 用 LLM 理解整个过程，做出判断
type Observation struct {
	// Statement：观察陈述（Executor 的发现）
	Statement string `json:"statement"`

	// Reasoning：Executor 的推理过程
	Reasoning string `json:"reasoning"`

	// Severity：严重性评估
	Severity string `json:"severity"` // low/medium/high/critical

	// Confidence：Executor 的置信度
	Confidence string `json:"confidence"` // low/medium/high

	// TestPlan：验证计划（兼容旧代码）
	TestPlan string `json:"test_plan,omitempty"`

	// Repro：复现配方（兼容旧代码，已废弃，使用 Trace）
	Repro json.RawMessage `json:"repro,omitempty"`

	// Trace：完整的执行轨迹（核心）
	// Evaluator 用这个完整复现 Executor 的执行环境
	Trace ExecutionTrace `json:"trace"`

	// ExecutionTrace：指针形式（用于动态附加）
	ExecutionTrace *ExecutionTrace `json:"execution_trace,omitempty"`
}

// ExecutionTrace 记录 Executor 执行时的完整轨迹
type ExecutionTrace struct {
	// Traffic：所有 HTTP 请求/响应（按时间顺序）
	Traffic []TrafficRecord `json:"traffic"`

	// Session：会话状态（cookies, headers）
	Session SessionState `json:"session"`

	// KeyRequests：关键请求的标记
	KeyRequests KeyRequestMarkers `json:"key_requests"`

	// Metadata：元数据
	Meta TraceMeta `json:"meta"`
}

// TrafficRecord 是单条 HTTP 记录
type TrafficRecord struct {
	// Seq：序号（从 0 开始）
	Seq int `json:"seq"`

	// ID：traffic ID
	ID string `json:"id"`

	// Request：HTTP 请求
	Request HTTPRequest `json:"request"`

	// Response：HTTP 响应
	Response HTTPResponse `json:"response"`

	// Tool：产生这个请求的工具
	Tool string `json:"tool,omitempty"`

	// Timestamp：时间戳
	Timestamp time.Time `json:"timestamp"`

	// Comment：注释（Executor 可以标注这个请求的用途）
	Comment string `json:"comment,omitempty"`
}

// HTTPRequest 是 HTTP 请求
type HTTPRequest struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
}

// HTTPResponse 是 HTTP 响应
type HTTPResponse struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
}

// SessionState 是会话状态
type SessionState struct {
	// Cookies：所有 cookies
	Cookies map[string]string `json:"cookies"`

	// Headers：默认 headers
	Headers map[string]string `json:"headers,omitempty"`

	// Host：目标主机
	Host string `json:"host,omitempty"`
}

// KeyRequestMarkers 标记关键请求
type KeyRequestMarkers struct {
	// Attack：攻击请求的序号（反射型 XSS, SQL 注入）
	Attack []int `json:"attack,omitempty"`

	// Baseline：基线请求的序号（用于对比）
	Baseline []int `json:"baseline,omitempty"`

	// Setup：准备步骤的序号（如登录、设置 security level）
	Setup []int `json:"setup,omitempty"`

	// Inject：注入请求的序号（存储型 XSS 写入）
	Inject []int `json:"inject,omitempty"`

	// Verify：验证请求的序号（存储型 XSS 读取）
	Verify []int `json:"verify,omitempty"`
}

// TraceMeta 是 trace 的元数据
type TraceMeta struct {
	ActionID   string        `json:"action_id,omitempty"`
	TaskID     string        `json:"task_id,omitempty"`
	StartTime  time.Time     `json:"start_time,omitempty"`
	EndTime    time.Time     `json:"end_time,omitempty"`
	Duration   time.Duration `json:"duration,omitempty"`
	TotalCount int           `json:"total_count"` // 总请求数
}

// TraceBuilder 用于构建 ExecutionTrace
type TraceBuilder struct {
	trace ExecutionTrace
}

// NewTraceBuilder 创建一个新的 TraceBuilder
func NewTraceBuilder() *TraceBuilder {
	return &TraceBuilder{
		trace: ExecutionTrace{
			Traffic: make([]TrafficRecord, 0),
			Session: SessionState{
				Cookies: make(map[string]string),
				Headers: make(map[string]string),
			},
			KeyRequests: KeyRequestMarkers{},
			Meta:        TraceMeta{StartTime: time.Now()},
		},
	}
}

// AddTraffic 添加一条 traffic 记录
// 返回这条记录的序号
func (b *TraceBuilder) AddTraffic(snapshot proxy.TrafficSnapshot, tool, comment string) int {
	seq := len(b.trace.Traffic)

	// 提取请求
	req := HTTPRequest{
		Method:  snapshot.Method,
		URL:     buildURL(snapshot),
		Headers: snapshot.RequestHeaders,
		Body:    string(snapshot.RequestBody),
	}

	// 提取响应
	resp := HTTPResponse{
		Status:  snapshot.StatusCode,
		Headers: flattenHeaders(snapshot.ResponseHeaders),
		Body:    string(snapshot.ResponseBody),
	}

	// 创建记录
	record := TrafficRecord{
		Seq:       seq,
		ID:        snapshot.ID,
		Request:   req,
		Response:  resp,
		Tool:      tool,
		Timestamp: snapshot.Timestamp,
		Comment:   comment,
	}

	b.trace.Traffic = append(b.trace.Traffic, record)
	b.trace.Meta.TotalCount++

	// 更新会话状态
	b.updateSession(snapshot)

	return seq
}

// MarkAttack 标记攻击请求
func (b *TraceBuilder) MarkAttack(seq int) {
	b.trace.KeyRequests.Attack = append(b.trace.KeyRequests.Attack, seq)
}

// MarkBaseline 标记基线请求
func (b *TraceBuilder) MarkBaseline(seq int) {
	b.trace.KeyRequests.Baseline = append(b.trace.KeyRequests.Baseline, seq)
}

// MarkSetup 标记准备步骤
func (b *TraceBuilder) MarkSetup(seq int) {
	b.trace.KeyRequests.Setup = append(b.trace.KeyRequests.Setup, seq)
}

// MarkInject 标记注入请求（存储型）
func (b *TraceBuilder) MarkInject(seq int) {
	b.trace.KeyRequests.Inject = append(b.trace.KeyRequests.Inject, seq)
}

// MarkVerify 标记验证请求（存储型）
func (b *TraceBuilder) MarkVerify(seq int) {
	b.trace.KeyRequests.Verify = append(b.trace.KeyRequests.Verify, seq)
}

// Build 完成构建并返回 trace
func (b *TraceBuilder) Build() ExecutionTrace {
	b.trace.Meta.EndTime = time.Now()
	b.trace.Meta.Duration = b.trace.Meta.EndTime.Sub(b.trace.Meta.StartTime)
	return b.trace
}

// GetTrace 返回当前的 trace（用于中途访问）
func (b *TraceBuilder) GetTrace() *ExecutionTrace {
	return &b.trace
}

// updateSession 从 traffic 中更新会话状态
func (b *TraceBuilder) updateSession(snapshot proxy.TrafficSnapshot) {
	// 从请求头中提取 cookies
	if cookie, ok := snapshot.RequestHeaders["cookie"]; ok {
		cookies := parseCookieHeader(cookie)
		for k, v := range cookies {
			b.trace.Session.Cookies[k] = v
		}
	}

	// 从响应头中提取 Set-Cookie
	if setCookies, ok := snapshot.ResponseHeaders["set-cookie"]; ok {
		for _, sc := range setCookies {
			name, value := parseSetCookie(sc)
			if name != "" {
				b.trace.Session.Cookies[name] = value
			}
		}
	}

	// 更新 host
	if b.trace.Session.Host == "" {
		b.trace.Session.Host = snapshot.Host
	}
}

// Helper functions

func buildURL(s proxy.TrafficSnapshot) string {
	scheme := s.Scheme
	if scheme == "" {
		scheme = "http"
	}
	url := scheme + "://" + s.Host
	if s.Path != "" {
		url += s.Path
	} else {
		url += "/"
	}
	return url
}

func flattenHeaders(headers map[string][]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	result := make(map[string]string, len(headers))
	for k, v := range headers {
		if len(v) > 0 {
			result[k] = v[0] // 只取第一个值
		}
	}
	return result
}

func parseCookieHeader(cookie string) map[string]string {
	cookies := make(map[string]string)
	// Cookie: name1=value1; name2=value2
	pairs := splitBySemicolon(cookie)
	for _, pair := range pairs {
		if idx := indexOfByte(pair, '='); idx > 0 {
			name := trim(pair[:idx])
			value := trim(pair[idx+1:])
			cookies[name] = value
		}
	}
	return cookies
}

func parseSetCookie(setCookie string) (name, value string) {
	// Set-Cookie: name=value; Path=/; HttpOnly
	if idx := indexOfByte(setCookie, ';'); idx > 0 {
		setCookie = setCookie[:idx]
	}
	if idx := indexOfByte(setCookie, '='); idx > 0 {
		name = trim(setCookie[:idx])
		value = trim(setCookie[idx+1:])
	}
	return
}

func splitBySemicolon(s string) []string {
	var result []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ';' {
			result = append(result, s[start:i])
			start = i + 1
			for start < len(s) && s[start] == ' ' {
				start++
			}
			i = start - 1
		}
	}
	if start < len(s) {
		result = append(result, s[start:])
	}
	return result
}

func indexOfByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

func trim(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}
