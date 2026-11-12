// 沙盒浏览器流量 → 流量字典的 HTTP 入口。
//
// 背景：executor 在沙盒里用 chromium 登录目标，真实认证请求（Document/XHR/Fetch）必须进
// agent_traffic 字典，LLM 才能看到真实请求结构 + 凭证位置 → 供 http_request / evaluator 复现门做
// 水平/垂直越权（BAC）测试。抓取前端是沙盒浏览器服务内建的 CDP Network observer（逐请求按
// session→tab→agent 归属），CLI 工具直连目标不抓。
//
// 本 handler 收到后构造 proxy.TrafficSnapshot{Source:"internal"} → Traffic.SubmitInternal
//
//	→ 进程内有界队列 → ingestor.drainInternal → handleInternalSnap 落库（不绕 Redis，自环 broker 冗余）。
//
// 路径：POST /internal/v1/traffic/ingest（挂在 runner 的 healthz HTTP mux 上）
// 认证：Bearer <sandbox.token>（与 runner 调沙盒同一共享密钥；沙盒经 sandbox.ingest_url 访问）

package runner

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/proxy"
)

// ingestRequest 是沙箱抓流量推送的 JSON 体。
//
// 与 proxy.TrafficSnapshot 字段一一对应但有两点差异：
//   - ID 由本 handler 生成（"cdp-<agentID>-<timestamp>"），不依赖 sandbox 端
//   - Duration 单位 ms（避免 nanosecond 跨语言 JSON 精度坑——python json.dumps int 安全）
//
// RequestBody/ResponseBody 是 []byte：JSON 里走 base64 字符串（Go encoding/json 约定，
// python 端 base64.b64encode）。空字符串 → 空 []byte。
type ingestRequest struct {
	AgentID         string              `json:"agent_id"`
	Identity        string              `json:"identity,omitempty"` // 身份名（browser 抓的填；CLI 空）
	Tool            string              `json:"tool,omitempty"`     // 发起工具（browser='browser'；CLI=UA 解析）
	Host            string              `json:"host"`
	HostPort        string              `json:"host_port,omitempty"`
	Method          string              `json:"method"`
	Scheme          string              `json:"scheme"`
	URI             string              `json:"uri"`
	Path            string              `json:"path"`
	Query           map[string][]string `json:"query,omitempty"`
	StatusCode      int                 `json:"status_code"`
	RequestHeaders  map[string]string   `json:"request_headers,omitempty"`
	RequestBody     []byte              `json:"request_body,omitempty"`
	ResponseHeaders map[string][]string `json:"response_headers,omitempty"`
	ResponseBody    []byte              `json:"response_body,omitempty"`
	DurationMs      int64               `json:"duration_ms,omitempty"`
	Timestamp       time.Time           `json:"timestamp,omitempty"`
}

// InternalSink 是 ingest_handler 对 ingestor 的窄依赖：把 internal 流量直接入进程内队列
// （*ingestor.Traffic 的 SubmitInternal 满足）。同进程直送，不再绕 redis。
type InternalSink interface {
	SubmitInternal(*proxy.TrafficSnapshot) bool
}

// NewIngestHandler 返回 沙盒抓流量 → 进程内 ingestor 队列的 HTTP handler。
// 强制 `Authorization: Bearer <token>`；token 为空时拒绝一切请求（fail-closed）。
//
// 错误响应：
//   - 405 method 非 POST
//   - 401 token 不匹配
//   - 400 JSON 解析失败 / agent_id 缺失
//   - 503 ingestor 队列已满（背压——沙箱应退避重试）
//   - 200 成功（不返 body，节省带宽）
func NewIngestHandler(sink InternalSink, token string, logger zerolog.Logger) http.HandlerFunc {
	configured := strings.TrimSpace(token) != ""
	expected := "Bearer " + token

	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		if !configured || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte(expected)) != 1 {
			logger.Warn().Str("remote", r.RemoteAddr).Msg("ingest 鉴权失败")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		var req ingestRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			logger.Warn().Err(err).Msg("ingest body 解析失败")
			http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
			return
		}
		if req.AgentID == "" {
			http.Error(w, "agent_id required", http.StatusBadRequest)
			return
		}

		ts := req.Timestamp
		if ts.IsZero() {
			ts = time.Now().UTC()
		}

		snap := &proxy.TrafficSnapshot{
			ID:              "cdp-" + req.AgentID + "-" + ts.Format("20060102T150405.000000000"),
			AgentRunID:      req.AgentID,
			Source:          "internal",
			Identity:        req.Identity,
			Tool:            req.Tool,
			Host:            req.Host,
			HostPort:        req.HostPort,
			Method:          req.Method,
			Scheme:          req.Scheme,
			URI:             req.URI,
			Path:            req.Path,
			Query:           req.Query,
			StatusCode:      req.StatusCode,
			RequestHeaders:  req.RequestHeaders,
			RequestBody:     req.RequestBody,
			ResponseHeaders: req.ResponseHeaders,
			ResponseBody:    req.ResponseBody,
			Duration:        time.Duration(req.DurationMs) * time.Millisecond,
			Timestamp:       ts,
		}

		if !sink.SubmitInternal(snap) {
			logger.Warn().Str("agent_id", req.AgentID).Msg("ingest 队列已满，背压 503（沙箱应退避重试）")
			http.Error(w, "ingest queue full", http.StatusServiceUnavailable)
			return
		}

		logger.Info().
			Str("agent_id", req.AgentID).
			Str("method", req.Method).
			Str("host", req.Host).
			Str("path", proxy.LogPath(req.URI)).
			Int("status", req.StatusCode).
			Msg("ingest accepted")

		w.WriteHeader(http.StatusOK)
	}
}
