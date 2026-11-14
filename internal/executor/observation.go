package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/V3teran/liusha/internal/evaluator"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/proxy"
	"github.com/V3teran/liusha/internal/repro"
)

// createObservationNode 为观察结果创建 observation 节点
//
// 验证状态由 Category 完全决定（不由 executor 赋值）：
// - hypothesis + repro → pending（待复现门验证）
// - 其余全部 → 没有验证状态（事实写入即终态：信任来自执行轨迹出处，无需验证）
func (e *Engine) createObservationNode(
	ctx context.Context,
	taskID string,
	actionID string,
	obs Observation,
) (*explorationgraph.Node, error) {
	// 构造 content
	contentMap := map[string]interface{}{
		"statement": obs.Statement,
		"category":  obs.Category,
		"reasoning": obs.Reasoning,
	}

	if obs.Priority != "" {
		contentMap["priority"] = obs.Priority
	}
	if obs.Impact != "" {
		contentMap["impact"] = obs.Impact
	}
	if obs.Suggestion != "" {
		contentMap["suggestion"] = obs.Suggestion
	}

	// 保存执行轨迹
	if obs.ExecutionTrace != nil {
		contentMap["execution_trace"] = obs.ExecutionTrace
	}

	// 保存复现配方（仅对 vulnerability 有意义）
	if len(obs.Repro) > 0 {
		normalized, _, nErr := repro.NormalizeReproEnvelope(obs.Repro)
		if nErr == nil {
			obs.Repro = normalized
		}
		contentMap["repro"] = obs.Repro
	}

	content, err := json.Marshal(contentMap)
	if err != nil {
		return nil, fmt.Errorf("marshal content: %w", err)
	}

	node := explorationgraph.Node{
		ID:         uuid.New().String(),
		TaskID:     taskID,
		Kind:       core.KindObservation,
		Content:    content,
		Priority:   explorationgraph.PriorityMedium,
		SourceType: explorationgraph.SourceExecutor,
		SourceID:   e.agentRunID,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}

	// 只有 hypothesis 进验证生命周期（pending → 复现门验证），其余五个信息域
	// 都是写入即终态的事实，不带验证状态。
	switch obs.Category {
	case explorationgraph.CategoryHypothesis:
		// hypothesis 必须有 repro
		if len(obs.Repro) == 0 {
			return nil, fmt.Errorf("hypothesis observation requires repro")
		}
		pending := explorationgraph.VerificationPending
		node.VerificationStatus = &pending

	case explorationgraph.CategoryEntity, explorationgraph.CategoryAccess, explorationgraph.CategoryStructure, explorationgraph.CategoryBehavior, explorationgraph.CategoryNote:

	default:
		return nil, fmt.Errorf("unknown observation category: %s", obs.Category)
	}

	// action → observation 关系边与节点原子落库
	if actionID != "" {
		edge := explorationgraph.Edge{
			TaskID:    taskID,
			SrcID:     actionID,
			Rel:       explorationgraph.RelGenerates,
			DstID:     node.ID,
			CreatedAt: time.Now(),
		}
		if err := e.graph.CreateNodeWithEdges(ctx, node, []explorationgraph.Edge{edge}); err != nil {
			return nil, fmt.Errorf("create node with edges: %w", err)
		}
		return &node, nil
	}

	if _, err := e.graph.CreateNode(ctx, node); err != nil {
		return nil, fmt.Errorf("create node: %w", err)
	}
	return &node, nil
}

// observationToAttempt 将 observation 节点转换为 Attempt
func (e *Engine) observationToAttempt(
	taskID string,
	node *explorationgraph.Node,
	obs Observation,
) (evaluator.Attempt, error) {
	// priority 缺省交领域交付层补（finding 映射处有默认）——通用执行层
	// 不制造领域默认值。
	priority := obs.Priority

	// 构造 Content：交付层（deliverClaim）按这些键解构坐实载荷——
	// 中立层词汇 priority/impact/suggestion，安全域交付时译作
	// severity/impact/remediation（域词归域）。executor 的执行轨迹随
	// 观察节点另存，不进主张载荷（体积与职责：轨迹属图，主张属台账）。
	content := map[string]interface{}{
		"summary":  obs.Statement,
		"priority": priority,
	}
	if obs.Impact != "" {
		content["impact"] = obs.Impact
	}
	if obs.Suggestion != "" {
		content["suggestion"] = obs.Suggestion
	}
	contentJSON, err := json.Marshal(content)
	if err != nil {
		return evaluator.Attempt{}, fmt.Errorf("marshal content: %w", err)
	}

	return evaluator.Attempt{
		TaskID:     taskID,
		NodeID:     node.ID,
		Kind:       "result", // 晋升为 result 节点
		Primitives: obs.Repro,
		Content:    contentJSON,
		Priority:   "medium",
	}, nil
}

// ─────────────────────────────────────────────────────────────
// 观察载荷与执行轨迹（Observation / ExecutionTrace / TraceBuilder）
// ─────────────────────────────────────────────────────────────

// Observation 是执行器从环境感知到的一条断言（Category 语义见 explorationgraph.ObservationCategory）。
// 验证状态由 createObservationNode 按 Category 设置，不由执行器给出。
type Observation struct {
	// Statement：观察的陈述
	Statement string `json:"statement"`

	// Category：观察分类（决定生命周期）
	Category explorationgraph.ObservationCategory `json:"category"`

	// Reasoning：为什么做出这个观察
	Reasoning string `json:"reasoning"`

	// Priority：主张的优先级（low/medium/high/critical）——领域通用词，
	// 与图级 Priority 同一概念（探索资源有限，轻重缓急任何领域都有）。
	Priority string `json:"priority,omitempty"`

	// Impact：主张为真时的影响与波及面（中性因果陈述）。
	Impact string `json:"impact,omitempty"`

	// Suggestion：处理建议（若要改变这一局面可以怎么做）。
	Suggestion string `json:"suggestion,omitempty"`

	// Repro：复现配方
	//   - hypothesis：必须（无配方的推测无从验证）
	//   - 其余类别：可选
	// 形式：JSON，由域定义（web=HTTP请求序列，generic=自由形式等）
	Repro json.RawMessage `json:"repro,omitempty"`

	// ExecutionTrace：完整执行轨迹（所有请求响应、会话状态、元数据）——
	// 假设的判定依据，事实的出处
	ExecutionTrace *ExecutionTrace `json:"execution_trace,omitempty"`
}

// ExecutionTrace 记录 Executor 执行时的完整轨迹
type ExecutionTrace struct {
	// Traffic：所有 HTTP 请求/响应（按时间顺序）
	Traffic []TrafficRecord `json:"traffic"`

	// Session：会话状态（cookies, headers）
	Session SessionState `json:"session"`

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

// TraceMeta 是 trace 的元数据
type TraceMeta struct {
	ActionID     string    `json:"action_id,omitempty"`
	TaskID       string    `json:"task_id,omitempty"`
	StartTime    time.Time `json:"start_time,omitempty"`
	EndTime      time.Time `json:"end_time,omitempty"`
	DurationMs   int64     `json:"duration_ms,omitempty"` // 轨迹墙钟耗时（毫秒）
	RequestCount int       `json:"request_count"`         // 轨迹内 HTTP 请求条数
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
			Meta: TraceMeta{StartTime: time.Now()},
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
	b.trace.Meta.RequestCount++

	// 更新会话状态
	b.updateSession(snapshot)

	return seq
}

// Build 完成构建并返回 trace
func (b *TraceBuilder) Build() ExecutionTrace {
	b.trace.Meta.EndTime = time.Now()
	b.trace.Meta.DurationMs = b.trace.Meta.EndTime.Sub(b.trace.Meta.StartTime).Milliseconds()
	return b.trace
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
	// 请求头格式形如「name1=value1; name2=value2」，按分号切分后逐对取键值
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
