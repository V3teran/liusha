// ingest_handler.go — chromium CDP capture → 流量字典的 HTTP 入口。
//
// 背景（v33+ 撤回 chromium 经 martian proxy 路径）：
//   - chromium 经 sanitizer 8890 proxy 反复失败（HTTPS-First Mode / Fetch.authRequired /
//     CDP inject 多层 bug + macOS Docker NAT），v29-v32 全部踩坑
//   - 改走 CDP Network domain 主动抓 chromium 全量 req/resp，pentools/cdp_network_capture.py
//     在容器内监听 → 序列化 JSON → POST 此 endpoint
//   - 本 handler 收到后构造 proxy.TrafficSnapshot{Source:"internal"} → publisher.Publish
//     → XADD stream → ingestor.handleInternalSnap 自动消费（与 sanitizer 路径同下游）
//
// 路径：POST /internal/v1/flows/ingest（挂在 cmd/proxy healthz HTTP mux 上）
// 认证：Bearer <token>（token 空 = 开发模式不强制；prod 由 ENV LIUSHA_INGEST_TOKEN 注入）
// 网络：cmd/proxy healthz 监听 :9091，sandbox 容器经 host.docker.internal:9091 访问
package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/proxy"
)

// ingestRequest 是 chromium CDP capture 推送的 JSON 体。
//
// 与 proxy.TrafficSnapshot 字段一一对应但有两点差异：
//   - ID 由本 handler 生成（"cdp-<hunterID>-<timestamp>"），不依赖 pentools 端
//   - Duration 单位 ms（避免 nanosecond 跨语言 JSON 精度坑——python json.dumps int 安全）
type ingestRequest struct {
	HunterID        string              `json:"hunter_id"`
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

// newIngestHandler 返回 CDP capture → stream 的 HTTP handler。
//
// token 空时跳过认证（开发模式）；非空时强制 `Authorization: Bearer <token>`。
//
// 错误响应：
//   - 405 method 非 POST
//   - 401 token 不匹配（仅 token 非空时）
//   - 400 JSON 解析失败 / hunter_id 缺失
//   - 500 publisher.Publish 失败（redis 不可达等）
//   - 200 成功（不返 body，节省带宽）
func newIngestHandler(pub *proxy.Publisher, token string, logger zerolog.Logger) http.HandlerFunc {
	tokenRequired := strings.TrimSpace(token) != ""
	if !tokenRequired {
		logger.Warn().Msg("ingest_token 未配置，CDP ingest endpoint 不强制验证（开发模式；prod 必须设 ENV LIUSHA_INGEST_TOKEN）")
	}
	expected := "Bearer " + token

	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		if tokenRequired && r.Header.Get("Authorization") != expected {
			logger.Warn().Str("remote", r.RemoteAddr).Msg("CDP ingest 鉴权失败")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		var req ingestRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			logger.Warn().Err(err).Msg("CDP ingest body 解析失败")
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

		if err := pub.Publish(r.Context(), snap); err != nil {
			logger.Warn().Err(err).Str("hunter_id", req.HunterID).Msg("CDP ingest publish 失败")
			http.Error(w, "publish failed", http.StatusInternalServerError)
			return
		}

		logger.Debug().
			Str("hunter_id", req.HunterID).
			Str("method", req.Method).
			Str("host", req.Host).
			Str("uri", req.URI).
			Int("status", req.StatusCode).
			Msg("CDP ingest published")

		w.WriteHeader(http.StatusOK)
	}
}
