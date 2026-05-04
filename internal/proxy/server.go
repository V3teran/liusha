package proxy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/filter"
	"github.com/projectdiscovery/martian/v3"
	"github.com/projectdiscovery/proxify"
	"github.com/projectdiscovery/proxify/pkg/certs"
	"github.com/projectdiscovery/proxify/pkg/logger/elastic"
	"github.com/projectdiscovery/proxify/pkg/logger/kafka"
	"github.com/projectdiscovery/proxify/pkg/types"
	"github.com/rs/zerolog"
)

// 默认值（避免 magic numbers 散落各处）。
const (
	defaultListenAddr = "0.0.0.0:8888"
	defaultCertSubdir = ".liusha"
)

// Server 把 proxify SDK 当作进程内 MITM 代理，OnResponseCallback 触发 filter→snapshot→publisher（XADD）。
//
// 设计要点（业界最佳实践 - Stream-based）：
//   - 不输出 jsonl 文件，所有切片留在内存（OutputFile/OutputJsonl 关闭）。
//   - body 在回调中读完必须重建，否则下游客户端拿不到响应。
//   - 过滤通过后立即 XADD 到 Redis Stream；切窗 / 持久化 / 入队由消费者负责（解耦 + 无状态 proxy）。
// SnapshotPublisher 抽象 publish 行为，便于单元测试不依赖 Redis。
// 生产实现：proxy.Publisher（XADD 到 Redis Stream）。
type SnapshotPublisher interface {
	Publish(ctx context.Context, snap *TrafficSnapshot) error
}

type Server struct {
	proxy      *proxify.Proxy
	filter     *filter.TrafficFilter
	publisher  SnapshotPublisher
	cfg        config.ProxyConfig
	listenAddr string
	certDir    string
	logger     zerolog.Logger
}

// ServerDeps 注入服务依赖；ListenAddr / CertDir 为空时走默认值。
type ServerDeps struct {
	Filter     *filter.TrafficFilter
	Publisher  SnapshotPublisher
	Cfg        config.ProxyConfig
	ListenAddr string
	CertDir    string
	Logger     zerolog.Logger
}

// NewServer 构造内嵌 MITM 代理；首次启动会在 CertDir 下生成 CA。
func NewServer(deps ServerDeps) (*Server, error) {
	if deps.Filter == nil {
		return nil, errors.New("proxy.NewServer: Filter 必填")
	}
	if deps.Publisher == nil {
		return nil, errors.New("proxy.NewServer: Publisher 必填")
	}

	listenAddr := strings.TrimSpace(deps.ListenAddr)
	if listenAddr == "" {
		listenAddr = defaultListenAddr
	}

	certDir, err := resolveCertDir(deps.CertDir)
	if err != nil {
		return nil, fmt.Errorf("解析证书目录失败: %w", err)
	}
	if err := os.MkdirAll(certDir, 0o755); err != nil {
		return nil, fmt.Errorf("创建证书目录 %s 失败: %w", certDir, err)
	}
	// 首次启动会自动生成 CA；已存在则就地加载
	if err := certs.LoadCerts(certDir); err != nil {
		return nil, fmt.Errorf("加载/生成 CA 证书失败: %w", err)
	}

	srv := &Server{
		filter:     deps.Filter,
		publisher:  deps.Publisher,
		cfg:        deps.Cfg,
		listenAddr: listenAddr,
		certDir:    certDir,
		logger:     deps.Logger,
	}

	opts := &proxify.Options{
		ListenAddrHTTP:     listenAddr,
		Directory:          certDir,
		OutputFile:         "", // 关闭 jsonl 文件输出（彻底 in-process）
		OutputJsonl:        false,
		DumpRequest:        false,
		DumpResponse:       false,
		Verbosity:          types.VerbositySilent,
		OnResponseCallback: srv.onResponse,
		// 必填空指针：proxify NewLogger 内部直接读 .Addr，nil 会 panic
		Elastic: &elastic.Options{},
		Kafka:   &kafka.Options{},
	}

	p, err := proxify.NewProxy(opts)
	if err != nil {
		return nil, fmt.Errorf("创建 proxify 实例失败: %w", err)
	}
	srv.proxy = p

	return srv, nil
}

// onResponse 是 proxify 的响应回调：
//  1. 走 TrafficFilter；不通过即丢弃（return nil 不影响转发）。
//  2. 读取 req/resp body 并重建（必须，否则代理会断），构造 TrafficSnapshot。
//  3. XADD 到 Redis Stream（FlowStream）；publish 失败仅 warn 不阻断转发。
func (s *Server) onResponse(resp *http.Response, _ *martian.Context) error {
	if resp == nil || resp.Request == nil {
		return nil
	}

	req := resp.Request

	// 1) 过滤
	if ok, reason := s.filter.ShouldProcess(req, resp); !ok {
		s.logger.Debug().
			Str("method", req.Method).
			Str("host", req.URL.Host).
			Str("path", req.URL.Path).
			Int("status", resp.StatusCode).
			Str("reason", reason).
			Msg("流量被过滤")
		return nil
	}

	// 2) 读+重建 body（请求 / 响应都要）
	reqBody, err := readAndRebuildBody(&req.Body, s.cfg.MaxRequestBodySize)
	if err != nil {
		s.logger.Warn().Err(err).Msg("读取请求体失败，跳过该流量")
		return nil
	}
	respBody, err := readAndRebuildBody(&resp.Body, s.cfg.MaxResponseBodySize)
	if err != nil {
		s.logger.Warn().Err(err).Msg("读取响应体失败，跳过该流量")
		return nil
	}

	// 3) 构造 snapshot 并投递到 Stream
	snap := buildSnapshot(req, resp, reqBody, respBody)
	if err := s.publisher.Publish(req.Context(), snap); err != nil {
		s.logger.Warn().Err(err).
			Str("method", snap.Method).Str("host", snap.Host).Str("uri", snap.URI).
			Msg("Publisher.Publish 失败（流量已丢弃）")
		return nil
	}
	s.logger.Debug().
		Str("method", snap.Method).Str("host", snap.Host).Str("uri", snap.URI).
		Int("status", snap.StatusCode).Msg("flow 已投递到 stream")
	return nil
}

// Run 启动 proxify（阻塞 HTTP listener），ctx 取消时 stop。
func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		errCh <- s.proxy.Run()
	}()

	s.logger.Info().Str("addr", s.listenAddr).Str("cert_dir", s.certDir).Msg("MITM 代理已启动")

	select {
	case <-ctx.Done():
		s.Stop()
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}

// Stop 停止 proxify。可重复调用。
//
// 注意：proxify v0.0.16 的 Stop() 是 no-op，listener 没有显式关闭点；
// 因此调用方应在进程退出时依赖 listener 随进程关闭。
// Publisher 是无状态的，无需关闭（rdb 由调用方关）。
func (s *Server) Stop() {
	if s.proxy != nil {
		s.proxy.Stop()
	}
}

// resolveCertDir 把 ~ 展开成 $HOME/.liusha；空字符串走默认。
func resolveCertDir(dir string) (string, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, defaultCertSubdir), nil
	}
	if strings.HasPrefix(dir, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, dir[2:]), nil
	}
	return dir, nil
}

// readAndRebuildBody 读完 body 并重建 io.NopCloser，保证下游可继续读取。
//
//	maxSize > 0 时使用 LimitReader 截断，超过部分丢弃；
//	maxSize <= 0 表示不限。
//	返回的 []byte 是新切片，安全独立持有。
func readAndRebuildBody(bodyPtr *io.ReadCloser, maxSize int) ([]byte, error) {
	if bodyPtr == nil || *bodyPtr == nil {
		return nil, nil
	}
	src := *bodyPtr
	defer src.Close()

	var reader io.Reader = src
	if maxSize > 0 {
		reader = io.LimitReader(src, int64(maxSize))
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	*bodyPtr = io.NopCloser(bytes.NewReader(data))
	return data, nil
}

// buildSnapshot 把 net/http 请求 + 响应 + 已读 body 拼成 TrafficSnapshot。
func buildSnapshot(req *http.Request, resp *http.Response, reqBody, respBody []byte) *TrafficSnapshot {
	hostPort := req.URL.Host
	if hostPort == "" {
		hostPort = req.Host
	}
	if hostPort == "" {
		hostPort = req.Header.Get("Host")
	}
	host := stripPort(hostPort)

	scheme := strings.ToLower(req.URL.Scheme)
	if scheme == "" {
		if req.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}

	uri := req.URL.RequestURI()
	if uri == "" {
		uri = req.URL.Path
	}

	// 拆解：path 用于 dedup 模板化；query 多值 map 用于 sniffer/replay 结构化操作。
	// req.URL.Query() 返回 url.Values（即 map[string][]string）；空 query 时不写入字段（json omitempty）。
	var query map[string][]string
	if q := req.URL.Query(); len(q) > 0 {
		query = q
	}

	return &TrafficSnapshot{
		ID:              generateSnapshotID(req.Method, host, uri, reqBody),
		Host:            host,
		HostPort:        hostPort,
		Method:          req.Method,
		Scheme:          scheme,
		URI:             uri,
		Path:            req.URL.Path,
		Query:           query,
		StatusCode:      resp.StatusCode,
		RequestHeaders:  flattenRequestHeaders(req.Header),
		ResponseHeaders: flattenResponseHeaders(resp.Header),
		RequestBody:     reqBody,
		ResponseBody:    respBody,
		Timestamp:       time.Now().UTC(),
	}
}

// flattenRequestHeaders 把请求头拍平成 map[string]string，多值用 "," join。
// HTTP/1.1 请求头无多 header 行场景（Cookie/Accept 等内部已用 ; 或 , 分隔），
// 用 string 简化下游使用（sniffer 不需要类型断言）。key 全部小写化便于匹配。
func flattenRequestHeaders(h http.Header) map[string]string {
	if len(h) == 0 {
		return nil
	}
	out := make(map[string]string, len(h))
	for k, vs := range h {
		key := strings.ToLower(k)
		if len(vs) == 1 {
			out[key] = vs[0]
			continue
		}
		out[key] = strings.Join(vs, ",")
	}
	return out
}

// flattenResponseHeaders 保留响应头多值切片，与 http.Header 原 shape 对齐。
//
// 关键原因：Set-Cookie 按 RFC 6265 必须独立多行，且 cookie value 内允许逗号 —— ", " join
// 会让 "a=1; Path=/, b=2; Path=/" 形态的拼接破坏后续 cookie 解析。这里深拷贝切片，
// 避免下游意外修改影响 http.Header 内部状态。key 全部小写化便于匹配。
func flattenResponseHeaders(h http.Header) map[string][]string {
	if len(h) == 0 {
		return nil
	}
	out := make(map[string][]string, len(h))
	for k, vs := range h {
		key := strings.ToLower(k)
		cp := make([]string, len(vs))
		copy(cp, vs)
		out[key] = cp
	}
	return out
}

// stripPort 去掉 host 末尾的端口（IPv6 暂只处理 ":port" 形式，liusha 流量场景足够）。
func stripPort(host string) string {
	if host == "" {
		return host
	}
	// IPv6 地址形如 [::1]:8080
	if strings.HasPrefix(host, "[") {
		if idx := strings.LastIndex(host, "]"); idx > 0 {
			return host[:idx+1]
		}
	}
	if idx := strings.LastIndex(host, ":"); idx > 0 {
		// 排除 scheme 中的 ":"
		if !strings.ContainsRune(host[idx+1:], ':') {
			return host[:idx]
		}
	}
	return host
}

// generateSnapshotID = sha256(method | host | uri | body) 的十六进制摘要，
// 便于跨进程识别同一条流量。
func generateSnapshotID(method, host, uri string, body []byte) string {
	h := sha256.New()
	h.Write([]byte(method))
	h.Write([]byte("|"))
	h.Write([]byte(host))
	h.Write([]byte("|"))
	h.Write([]byte(uri))
	h.Write([]byte("|"))
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}
