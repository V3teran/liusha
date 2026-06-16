// ingest_handler.go — active 沙箱抓流量 → 流量字典的 HTTP 入口（从 cmd/proxy 迁来）。
//
// 背景：active 模式下 orchestrator/exploitation 在沙箱里用 chromium 登录目标 + 跑 CLI 工具，
// 真实认证请求（Document/XHR/Fetch）必须进 http_flow 字典，LLM 才能看到真实请求结构 + 凭证位置
// → 转 replay_flow 做水平/垂直越权（BAC）测试。
//
// 两条抓取前端（都在沙箱内，都 POST 到这里）：
//   - 浏览器：browser-svc.py 内建 CDP Network observer（per-request 按 session→tab→hunter 归属）
//   - CLI：容器内本地 mitmdump + mitm-capture.py，工具经 HTTP_PROXY 走它（owner 级归属，hunter_id 走 env）
//
// 本 handler 收到后构造 proxy.TrafficSnapshot{Source:"internal"} → publisher.Publish
//
//	→ XADD flow_events stream → ingestor.handleInternalSnap 消费（与 sanitizer 路径同下游）。
//
// 路径：POST /internal/v1/flows/ingest（挂在 cmd/scanner healthz HTTP mux 上，默认 :9090）
// 认证：Bearer <token>（token 空 = 开发模式不强制；prod 由 ENV LIUSHA_INGEST_TOKEN 注入）
// 网络：scanner healthz 监听 :9090，沙箱容器经 host.docker.internal:9090 访问
package main

import (
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
//   - ID 由本 handler 生成（"cdp-<hunterID>-<timestamp>"），不依赖 pentools 端
//   - Duration 单位 ms（避免 nanosecond 跨语言 JSON 精度坑——python json.dumps int 安全）
//
// RequestBody/ResponseBody 是 []byte：JSON 里走 base64 字符串（Go encoding/json 约定，
// python 端 base64.b64encode）。空字符串 → 空 []byte。
type ingestRequest struct {
	HunterID        string              `json:"hunter_id"`
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

// internalSink 是 ingest_handler 对 ingestor 的窄依赖：把 internal 流量直接入进程内队列
// （*ingestor.Traffic 的 SubmitInternal 满足）。同进程直送，不再绕 redis。
type internalSink interface {
	SubmitInternal(*proxy.TrafficSnapshot) bool
}

// newIngestHandler 返回 沙箱抓流量 → 进程内 ingestor 队列的 HTTP handler。
//
// token 空时跳过认证（开发模式）；非空时强制 `Authorization: Bearer <token>`。
//
// 错误响应：
//   - 405 method 非 POST
//   - 401 token 不匹配（仅 token 非空时）
//   - 400 JSON 解析失败 / hunter_id 缺失
//   - 503 ingestor 队列已满（背压——沙箱应退避重试）
//   - 200 成功（不返 body，节省带宽）
func newIngestHandler(sink internalSink, token string, logger zerolog.Logger) http.HandlerFunc {
	tokenRequired := strings.TrimSpace(token) != ""
	if !tokenRequired {
		logger.Warn().Msg("ingest_token 未配置，ingest endpoint 不强制验证（开发模式；prod 必须设 ENV LIUSHA_INGEST_TOKEN）")
	}
	expected := "Bearer " + token

	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		if tokenRequired && r.Header.Get("Authorization") != expected {
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
		if req.HunterID == "" {
			http.Error(w, "hunter_id required", http.StatusBadRequest)
			return
		}

		ts := req.Timestamp
		if ts.IsZero() {
			ts = time.Now().UTC()
		}

		snap := &proxy.TrafficSnapshot{
			ID:              "cdp-" + req.HunterID + "-" + ts.Format("20060102T150405.000000000"),
			HunterID:        req.HunterID,
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
			logger.Warn().Str("hunter_id", req.HunterID).Msg("ingest 队列已满，背压 503（沙箱应退避重试）")
			http.Error(w, "ingest queue full", http.StatusServiceUnavailable)
			return
		}

		logger.Info().
			Str("hunter_id", req.HunterID).
			Str("method", req.Method).
			Str("host", req.Host).
			Str("uri", req.URI).
			Int("status", req.StatusCode).
			Msg("ingest accepted")

		w.WriteHeader(http.StatusOK)
	}
}
